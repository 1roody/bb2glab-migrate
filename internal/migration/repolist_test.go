package migration

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ---- utilidades
func TestNormPath(t *testing.T) {
	for in, want := range map[string]string{
		"/exemplo/grupo": "exemplo/grupo",
		"https://gitlab.com/exemplo/grupo/proj.git": "exemplo/grupo/proj",
		"https://user@bitbucket.org/ws/repo.git":    "ws/repo",
		"  https://www.bitbucket.org/ws/repo/  ":    "ws/repo",
	} {
		if got := normalizeRepoPath(in); got != want {
			t.Errorf("NormPath(%q) = %q, esperado %q", in, got, want)
		}
	}
}

func TestParseList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.txt")
	os.WriteFile(path, []byte("# comentário\nws/a\nws/b  g/x/b2   # explícito\nws/c -> g/y/c\n\nws/d a b\nws/e g\n"), 0o644)
	repos, errs, err := parseRepoList(path, "base/sub")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, repo := range repos {
		got = append(got, repo.Source+" "+repo.Dest)
	}
	if !reflect.DeepEqual(got, []string{"ws/a base/sub/a", "ws/b g/x/b2", "ws/c g/y/c"}) {
		t.Fatalf("repos = %v", got)
	}
	if len(errs) != 2 || !strings.HasPrefix(errs[0], "linha 6:") || !strings.Contains(errs[1], "destino inválido") {
		t.Fatalf("errs = %v", errs)
	}
	_, errs, _ = parseRepoList(path, "")
	if !strings.Contains(strings.Join(errs, "\n"), "GITLAB_DEST_PATH não definido") {
		t.Fatalf("errs = %v", errs)
	}
}

func TestDestinosDuplicadosSemDiferenciarMaiusculas(t *testing.T) {
	es := []*Repo{newRepo("a/x", "g/Proj", 1), newRepo("a/y", "g/proj", 2), newRepo("a/z", "g/z", 3)}
	if got := findDuplicateDests(es); !reflect.DeepEqual(got, []string{"g/Proj = g/proj"}) {
		t.Fatalf("dups = %v", got)
	}
}

func TestNomeDeProjetoNovo(t *testing.T) {
	if p := invalidProjectName("deng-ae_baseunicapf-ygg"); p != "" {
		t.Fatal(p)
	}
	for _, bad := range []string{"-x", "x-", "a--b", "a_.b", "x.git", "com espaço"} {
		if invalidProjectName(bad) == "" {
			t.Errorf("%q devia ser inválido", bad)
		}
	}
}

func TestCodigosDeSaida(t *testing.T) {
	repo := newRepo("a/x", "g/x", 1)
	repo.Action = ActionDone
	ok := &Result{Repo: repo, Status: StatusComplete}
	part := &Result{Repo: repo, Status: StatusPartial}
	fail := &Result{Repo: repo, Status: StatusFailed}
	div := newRepo("a/y", "g/y", 2)
	div.Action = ActionDivergent
	for _, c := range []struct {
		results []*Result
		repos   []*Repo
		want    int
	}{
		{[]*Result{ok}, []*Repo{repo}, 0},
		{[]*Result{ok, part}, []*Repo{repo}, 3},
		{[]*Result{part, fail}, []*Repo{repo}, 1},
		{nil, []*Repo{div}, 1},
	} {
		if got := exitCode(c.results, c.repos); got != c.want {
			t.Errorf("ExitCode = %d, esperado %d", got, c.want)
		}
	}
}

func TestLoadEnvFileNaoSobrescreveAmbiente(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("# x\nBB2GL_A=\"um\"\nexport BB2GL_B='dois'\nBB2GL_C=tres\n"), 0o644)
	t.Setenv("BB2GL_C", "ambiente")
	t.Setenv("BB2GL_A", "")
	os.Unsetenv("BB2GL_A")
	t.Setenv("BB2GL_B", "")
	os.Unsetenv("BB2GL_B")
	if err := loadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("BB2GL_A") != "um" || os.Getenv("BB2GL_B") != "dois" || os.Getenv("BB2GL_C") != "ambiente" {
		t.Fatalf("A=%q B=%q C=%q", os.Getenv("BB2GL_A"), os.Getenv("BB2GL_B"), os.Getenv("BB2GL_C"))
	}
}

func TestParseArgsOpcoesAntesEDepoisDaLista(t *testing.T) {
	o, err := parseOptions([]string{"-n", "outra.txt", "--yes", "--search", "a", "--search=b", "--import-timeout", "9"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.DryRun || !o.Yes || o.ListPath != "outra.txt" || !reflect.DeepEqual(o.Search, []string{"a", "b"}) || o.ImportTimeout != 9 {
		t.Fatalf("opts = %+v", o)
	}
	if o, _ := parseOptions(nil); o.ListPath != "repos.txt" {
		t.Fatalf("lista padrão = %q", o.ListPath)
	}
	if _, err := parseOptions([]string{"a.txt", "b.txt"}); err == nil {
		t.Fatal("dois arquivos deviam ser recusados")
	}
}
