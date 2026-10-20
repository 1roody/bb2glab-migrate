package migration

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/1roody/bb2glab-migrate/internal/gitlab"
)

// ---- fluxo completo (RunCLI): credenciais do importador e confirmação
type runCfg struct {
	args        []string
	bbStatus    int // status da API do Bitbucket para o repositório
	prStatus    int // status da API do Bitbucket para os pull requests
	gitlabToken string
	hasGlab     bool
	stdinTTY    bool
	list        string // conteúdo da lista (padrão: "ws/repo\n")
}

func (s *testEnv) run(cfg runCfg) (int, string) {
	t := s.t
	if cfg.bbStatus == 0 {
		cfg.bbStatus = 200
	}
	if cfg.prStatus == 0 {
		cfg.prStatus = 200
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/pullrequests") {
			w.WriteHeader(cfg.prStatus)
		} else {
			w.WriteHeader(cfg.bbStatus)
		}
	}))
	t.Cleanup(srv.Close)

	envFile := filepath.Join(s.root, "test.env")
	list := filepath.Join(s.root, "lista.txt")
	os.WriteFile(envFile, nil, 0o644)
	if cfg.list == "" {
		cfg.list = "ws/repo\n"
	}
	os.WriteFile(list, []byte(cfg.list), 0o644)
	t.Setenv("BITBUCKET_USER", "eu@empresa.com")
	t.Setenv("BITBUCKET_TOKEN", "t")
	t.Setenv("BITBUCKET_GIT_USER", "")
	t.Setenv("GITLAB_DEST_PATH", ns)
	t.Setenv("GITLAB_TOKEN", cfg.gitlabToken)

	var out bytes.Buffer
	deps := Dependencies{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out, StdinTTY: cfg.stdinTTY,
		LookPath: func(tool string) (string, error) {
			if tool == "glab" && !cfg.hasGlab {
				return "", errors.New("not found")
			}
			return "/usr/bin/" + tool, nil
		},
		NewGitLabClient: func(string, string) gitlab.API { return s.gl },
		GitLabHost:      gitlab.DefaultHost,
		BitbucketAPI:    srv.URL,
		BitbucketGit:    s.bbRoot,
		PollInterval:    time.Nanosecond,
	}
	args := append([]string{list, "--env-file", envFile, "--log", filepath.Join(s.root, "t.log"),
		"--existing-out", filepath.Join(s.root, "urls.txt"), "--results-dir", filepath.Join(s.root, "results")}, cfg.args...)
	code := RunCLI(context.Background(), args, deps)
	return code, out.String()
}

func TestRunImportadorIndisponivelParaAntesDeAlterar(t *testing.T) {
	s := newTestEnv(t)
	code, out := s.run(runCfg{args: []string{"--yes"}, prStatus: 403, hasGlab: true})
	if code != 2 || !strings.Contains(out, "--allow-git-only") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if s.called("POST", "import/bitbucket") || len(s.gl.projects) != 0 {
		t.Fatal("nada devia ser alterado")
	}
}

func TestRunCredenciaisDoBitbucketRecusadasParamAntesDaVerificacao(t *testing.T) {
	s := newTestEnv(t)
	code, out := s.run(runCfg{args: []string{"--yes"}, bbStatus: 401, hasGlab: true})
	if code != 2 || !strings.Contains(out, "HTTP 401") || strings.Contains(out, "Verificação") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestRunImportadorIndisponivelComAllowGitOnly(t *testing.T) {
	s := newTestEnv(t)
	code, out := s.run(runCfg{args: []string{"--yes", "--allow-git-only"}, prStatus: 403, hasGlab: true})
	if code != 3 { // parcial: Git verificado, sem PRs
		t.Fatalf("code=%d\n%s", code, out)
	}
	eqRefs(t, s.destRefs(), s.srcSHAs)
}

func TestRunReimportExigeImportadorMesmoComAllow(t *testing.T) {
	s := newTestEnv(t)
	s.gitOnlyProject()
	code, out := s.run(runCfg{args: []string{"--yes", "--reimport", "--allow-git-only"}, prStatus: 403, hasGlab: true})
	if code != 2 || s.gl.projects[ns+"/repo"].PathNS != ns+"/repo" {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestRunSemTerminalExigeYes(t *testing.T) {
	s := newTestEnv(t)
	code, out := s.run(runCfg{hasGlab: true})
	if code != 130 || !strings.Contains(out, "--yes") || len(s.gl.projects) != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestRunConfirmacaoInterativaNo(t *testing.T) {
	s := newTestEnv(t)
	code, out := s.run(runCfg{hasGlab: true, stdinTTY: true}) // stdin vazio: EOF conta como "no"
	if code != 130 || len(s.gl.projects) != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestRunFluxoCompletoComYes(t *testing.T) {
	s := newTestEnv(t)
	code, out := s.run(runCfg{args: []string{"--yes"}, hasGlab: true})
	if code != 0 || !strings.Contains(out, "Completos") || !strings.Contains(out, "(glab)") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	log, err := os.ReadFile(filepath.Join(s.root, "t.log"))
	if err != nil || !strings.Contains(string(log), "import_status=finished") || !strings.Contains(string(log), "Verificação") {
		t.Fatalf("log incompleto (%v):\n%s", err, log)
	}
}

func TestRunDryRunNaoAltera(t *testing.T) {
	s := newTestEnv(t)
	code, out := s.run(runCfg{args: []string{"--dry-run"}, hasGlab: true})
	if code != 0 || !strings.Contains(out, "dry-run:") || len(s.gl.projects) != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestRunGitLabTokenDispensaOGlab(t *testing.T) {
	s := newTestEnv(t)
	code, out := s.run(runCfg{args: []string{"--yes"}, gitlabToken: "glpat-x"})
	if code != 0 || !strings.Contains(out, "(GITLAB_TOKEN)") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	eqRefs(t, s.destRefs(), s.srcSHAs)
}

func TestRunSemGlabESemGitLabToken(t *testing.T) {
	s := newTestEnv(t)
	code, out := s.run(runCfg{args: []string{"--yes"}})
	if code != 2 || !strings.Contains(out, "GITLAB_TOKEN") || len(s.gl.projects) != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(s.root, "results")); err == nil {
		t.Fatal("parou antes da verificação: não devia gravar resultado")
	}
}

func TestRunConcluidoSalvaURLs(t *testing.T) {
	s := newTestEnv(t)
	s.migrateRepo(s.checkedRepo("ws/repo"), "")
	code, out := s.run(runCfg{args: []string{"--yes"}, hasGlab: true})
	urls, _ := os.ReadFile(filepath.Join(s.root, "urls.txt"))
	if code != 0 || string(urls) != "https://gitlab.com/"+ns+"/repo\n" {
		t.Fatalf("code=%d urls=%q\n%s", code, urls, out)
	}
}
