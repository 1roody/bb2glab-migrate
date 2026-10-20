package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/1roody/bb2glab-migrate/internal/bitbucket"
	"github.com/1roody/bb2glab-migrate/internal/git"
	"github.com/1roody/bb2glab-migrate/internal/gitlab"
	"github.com/1roody/bb2glab-migrate/internal/terminal"
)

const ns = "grupo/sub"

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func makeBare(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, "", "init", "-q", "--bare", path)
	gitT(t, path, "config", "receive.advertisePushOptions", "true")
	return path
}

func commitT(t *testing.T, work, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, work, "add", ".")
	gitT(t, work, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", msg)
	return gitT(t, work, "rev-parse", "HEAD")
}

type fakeProject struct {
	ID              int
	PathNS          string
	Path, Name      string
	ImportStatus    string
	ImportType      string
	ImportError     string
	HTTPURL         string
	FailedRelations []any
	Archived        bool
}

type apiCall struct{ Method, Endpoint string }

// fakeGitLab responde como a API do GitLab para os endpoints usados pela migração.
type fakeGitLab struct {
	mu            sync.Mutex // a verificação consulta em paralelo
	t             *testing.T
	root, bbRoot  string
	projects      map[string]*fakeProject // caminho (minúsculas) -> projeto
	redirects     map[string]int          // caminho antigo -> id (projetos renomeados)
	namespaces    map[string]int
	importMode    string
	delayedDelete bool   // imita a exclusão adiada do gitlab.com
	err           string // simula 401/429/5xx em qualquer consulta de projeto
	calls         []apiCall
	nextID        int
	forceNonEmpty bool // empty_repo sempre false (repositório inacessível)
}

func newFakeGitLab(t *testing.T, root, bbRoot string) *fakeGitLab {
	return &fakeGitLab{t: t, root: root, bbRoot: bbRoot, projects: map[string]*fakeProject{},
		redirects: map[string]int{}, namespaces: map[string]int{ns: 10}, importMode: "success", nextID: 100}
}

func (f *fakeGitLab) addProject(path, importStatus, importType string) *fakeProject {
	f.nextID++
	delete(f.redirects, strings.ToLower(path))
	name := path[strings.LastIndex(path, "/")+1:]
	p := &fakeProject{ID: f.nextID, PathNS: path, Path: name, Name: name, ImportStatus: importStatus, ImportType: importType,
		HTTPURL: makeBare(f.t, filepath.Join(f.root, fmt.Sprintf("%s-%d.git", path, f.nextID)))}
	f.projects[strings.ToLower(path)] = p
	return p
}

func (f *fakeGitLab) view(p *fakeProject) json.RawMessage {
	empty := false
	if !f.forceNonEmpty {
		out, _ := exec.Command("git", "-C", p.HTTPURL, "for-each-ref").Output()
		empty = len(bytes.TrimSpace(out)) == 0
	}
	var importType any
	if p.ImportType != "" {
		importType = p.ImportType
	}
	b, _ := json.Marshal(map[string]any{"id": p.ID, "path_with_namespace": p.PathNS, "path": p.Path, "name": p.Name,
		"import_status": p.ImportStatus, "import_type": importType, "http_url_to_repo": p.HTTPURL,
		"web_url": "https://gitlab.com/" + p.PathNS, "import_error": p.ImportError, "empty_repo": empty, "archived": p.Archived})
	return b
}

func (f *fakeGitLab) byID(id int) *fakeProject {
	for _, p := range f.projects {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (f *fakeGitLab) lookup(key string) *fakeProject {
	var id int
	if _, err := fmt.Sscanf(key, "%d", &id); err == nil && fmt.Sprint(id) == key {
		return f.byID(id)
	}
	p := f.projects[strings.ToLower(key)]
	if rid, ok := f.redirects[strings.ToLower(key)]; p == nil && ok { // a API real segue o redirecionamento
		p = f.byID(rid)
	}
	return p
}

func (f *fakeGitLab) copyRefs(src string, p *fakeProject, refs ...string) {
	specs := refs
	if len(specs) == 0 {
		specs = []string{"refs/heads/*:refs/heads/*", "refs/tags/*:refs/tags/*"}
	}
	for i, s := range specs {
		if !strings.Contains(s, ":") {
			specs[i] = s + ":" + s
		}
	}
	gitT(f.t, filepath.Join(f.bbRoot, src+".git"), append([]string{"push", "-q", p.HTTPURL}, specs...)...)
}

func notFound(what string) error { return &gitlab.APIError{Message: "404 " + what + " Not Found"} }

func asJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func (f *fakeGitLab) Do(method, endpoint string, body map[string]any) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, apiCall{method, endpoint})
	endpoint, query, _ := strings.Cut(endpoint, "?")
	parts := strings.Split(endpoint, "/")
	switch {
	case endpoint == "user":
		return asJSON(map[string]any{"username": "tester"}), nil
	case endpoint == "import/bitbucket":
		return f.doImport(body)
	case endpoint == "projects" && method == "POST":
		for path, id := range f.namespaces {
			if id == body["namespace_id"] {
				return f.view(f.addProject(path+"/"+body["path"].(string), "none", "")), nil
			}
		}
		return nil, notFound("Namespace")
	case parts[0] == "namespaces":
		path, _ := url.PathUnescape(parts[1])
		if id, ok := f.namespaces[path]; ok {
			return asJSON(map[string]any{"id": id}), nil
		}
		return nil, notFound("Namespace")
	case parts[0] != "projects":
		f.t.Fatalf("endpoint inesperado: %s %s", method, endpoint)
	}
	if f.err != "" {
		return nil, &gitlab.APIError{Message: f.err}
	}
	key, _ := url.PathUnescape(parts[1])
	p := f.lookup(key)
	if p == nil {
		return nil, notFound("Project")
	}
	sub := ""
	if len(parts) > 2 {
		sub = parts[2]
	}
	switch {
	case sub == "import":
		return asJSON(map[string]any{"import_status": p.ImportStatus, "import_error": p.ImportError,
			"failed_relations": append([]any{}, p.FailedRelations...)}), nil
	case sub == "archive" || sub == "unarchive":
		p.Archived = sub == "archive"
		return f.view(p), nil
	case method == "DELETE":
		if f.delayedDelete && !strings.Contains(query, "permanently_remove=true") {
			return asJSON(map[string]any{"message": "202 Accepted"}), nil // só marcou: o caminho continua ocupado
		}
		delete(f.projects, strings.ToLower(p.PathNS))
		return asJSON(map[string]any{"message": "202 Accepted"}), nil
	case method == "PUT":
		if path, ok := body["path"].(string); ok {
			old := p.PathNS
			delete(f.projects, strings.ToLower(old))
			f.redirects[strings.ToLower(old)] = p.ID
			p.PathNS = old[:strings.LastIndex(old, "/")] + "/" + path
			p.Path, p.Name = path, path
			if name, ok := body["name"].(string); ok {
				p.Name = name
			}
			f.projects[strings.ToLower(p.PathNS)] = p
		}
	}
	return f.view(p), nil
}

func (f *fakeGitLab) doImport(body map[string]any) (json.RawMessage, error) {
	path := body["target_namespace"].(string) + "/" + body["new_name"].(string)
	if f.importMode == "fail_before" {
		return nil, &gitlab.APIError{Status: 422, Message: "422 Unprocessable Entity"}
	}
	if _, taken := f.projects[strings.ToLower(path)]; taken {
		return nil, &gitlab.APIError{Status: 400, Message: `{"name":["has already been taken"]}`}
	}
	p := f.addProject(path, "finished", "bitbucket")
	src := body["repo_path"].(string)
	switch f.importMode {
	case "success":
		f.copyRefs(src, p)
	case "partial":
		f.copyRefs(src, p, "refs/heads/main")
	case "fail_after":
		p.ImportStatus, p.ImportError = "failed", "Bitbucket API: 500"
	case "pending":
		p.ImportStatus = "started"
	case "failed_relations":
		f.copyRefs(src, p)
		p.FailedRelations = []any{map[string]any{"relation_key": "merge_requests", "exception_message": "x"}}
	}
	return asJSON(map[string]any{"id": p.ID, "full_path": path}), nil
}

// testEnv: um Bitbucket e um GitLab locais, com a origem ws/repo (main, dev e a tag v1).
type testEnv struct {
	t                           *testing.T
	root, bbRoot, work, workdir string
	gl                          *fakeGitLab
	bb                          *bitbucket.Client
	ui                          *terminal.UI
	out                         *bytes.Buffer
	srcSHAs                     git.Refs
	listRemoteRefs              func(string, []string) (git.Refs, error)
}

func newTestEnv(t *testing.T) *testEnv {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	s := &testEnv{t: t, root: root, bbRoot: filepath.Join(root, "bb"), work: filepath.Join(root, "work"),
		workdir: filepath.Join(root, "clones"), out: &bytes.Buffer{}, listRemoteRefs: git.ListRemoteRefs}
	s.gl = newFakeGitLab(t, filepath.Join(root, "gl"), s.bbRoot)
	s.bb = bitbucket.New("eu@empresa.com", "token", "")
	s.bb.GitBase = s.bbRoot
	s.ui = terminal.NewUI(s.out, s.out, false)
	if err := os.MkdirAll(s.workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	s.srcSHAs = s.makeSource("ws/repo")
	return s
}

func (s *testEnv) makeSource(src string) git.Refs {
	t := s.t
	bare := makeBare(t, filepath.Join(s.bbRoot, src+".git"))
	work := filepath.Join(s.work, src)
	gitT(t, "", "init", "-q", "-b", "main", work)
	c1 := commitT(t, work, "um")
	gitT(t, work, "tag", "v1")
	gitT(t, work, "checkout", "-q", "-b", "dev")
	c2 := commitT(t, work, "dois")
	gitT(t, work, "push", "-q", bare, "main", "dev", "v1")
	gitT(t, bare, "symbolic-ref", "HEAD", "refs/heads/main")
	return git.Refs{"refs/heads/main": c1, "refs/heads/dev": c2, "refs/tags/v1": c1}
}

type opt func(*Options)

func allowGitOnly(o *Options) { o.AllowGitOnly = true }

func force(o *Options) { o.Force = true }

func retryFailedImports(o *Options) { o.RetryFailedImports = true }

func reimport(o *Options) { o.Reimport = true }

func noTimeout(o *Options) { o.ImportTimeout = 0 }

func (s *testEnv) newMigration(opts ...opt) *Migration {
	o := &Options{ImportTimeout: 5}
	for _, fn := range opts {
		fn(o)
	}
	return &Migration{ctx: context.Background(), opts: o, ui: s.ui, bitbucket: s.bb,
		gitlab:         &gitlab.Client{API: s.gl, Host: gitlab.DefaultHost},
		deps:           Dependencies{Stdout: s.out, Stderr: s.out},
		listRemoteRefs: func(u string, env []string) (git.Refs, error) { return s.listRemoteRefs(u, env) }}
}

func (s *testEnv) checkedRepo(src string, opts ...opt) *Repo {
	repo := newRepo(src, ns+"/"+src[strings.Index(src, "/")+1:], 1)
	s.newMigration(opts...).checkAll([]*Repo{repo})
	return repo
}

func (s *testEnv) migrateRepo(repo *Repo, problem string, opts ...opt) *Result {
	return s.newMigration(opts...).migrateRepo(repo, s.workdir, problem)
}

func (s *testEnv) destRefs() git.Refs {
	p := s.gl.projects[ns+"/repo"]
	if p == nil {
		s.t.Fatalf("projeto %s/repo não existe", ns)
	}
	refs, err := git.ListRemoteRefs(p.HTTPURL, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	return refs
}

func (s *testEnv) createdEmpty() int {
	n := 0
	for _, c := range s.gl.calls {
		if c.Method == "POST" && c.Endpoint == "projects" {
			n++
		}
	}
	return n
}

func (s *testEnv) called(method, endpoint string) bool {
	for _, c := range s.gl.calls {
		if c.Method == method && c.Endpoint == endpoint {
			return true
		}
	}
	return false
}

// gitOnlyProject: projeto migrado antes só por Git (sem o importador, sem PRs).
func (s *testEnv) gitOnlyProject(refs ...string) *fakeProject {
	p := s.gl.addProject(ns+"/repo", "none", "")
	s.gl.copyRefs("ws/repo", p, refs...)
	return p
}

func eqRefs(t *testing.T, got, want git.Refs) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("refs\n got: %v\nwant: %v", got, want)
	}
}

func wantAction(t *testing.T, repo *Repo, want Action) {
	t.Helper()
	if repo.Action != want {
		t.Fatalf("ação = %q (%s), esperado %q", repo.Action, repo.Reason, want)
	}
}

func wantStatus(t *testing.T, r *Result, want Status) {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status = %q (api=%q git=%q), esperado %q", r.Status, r.ImportNote, r.GitNote, want)
	}
}
