package migration

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ---- verificação
func TestVerificacaoDestinoAusenteViraImportacao(t *testing.T) {
	wantAction(t, newTestEnv(t).checkedRepo("ws/repo"), ActionImport)
}

func TestVerificacaoGrupoDeDestinoAusenteBloqueia(t *testing.T) {
	s := newTestEnv(t)
	s.gl.namespaces = map[string]int{}
	repo := s.checkedRepo("ws/repo")
	wantAction(t, repo, ActionUnverified)
	if !strings.Contains(repo.Reason, "não encontrado") {
		t.Fatalf("reason = %q", repo.Reason)
	}
}

func TestVerificacaoErroDoGitLabNaoETratadoComoAusente(t *testing.T) {
	s := newTestEnv(t)
	s.gl.err = "401 Unauthorized"
	repo := s.checkedRepo("ws/repo")
	wantAction(t, repo, ActionUnverified)
	if exitCode(nil, []*Repo{repo}) != 1 {
		t.Fatal("bloqueio devia sair com 1")
	}
}

func TestVerificacaoErroDeLsRemoteNoDestinoEFalha(t *testing.T) {
	s := newTestEnv(t)
	p := s.gitOnlyProject()
	p.HTTPURL = filepath.Join(s.gl.root, "nao-existe.git")
	s.gl.forceNonEmpty = true
	repo := s.checkedRepo("ws/repo")
	wantAction(t, repo, ActionUnverified)
	if !strings.Contains(repo.Reason, "ls-remote") {
		t.Fatalf("reason = %q", repo.Reason)
	}
}

func TestVerificacaoErroDeLsRemoteNaOrigemNaoViraOrigemVazia(t *testing.T) {
	s := newTestEnv(t)
	repo := s.checkedRepo("ws/inexistente")
	wantAction(t, repo, ActionUnverified)
	if !strings.Contains(repo.Reason, "Bitbucket") {
		t.Fatalf("reason = %q", repo.Reason)
	}
}

func TestVerificacaoOrigemVaziaEIgnorada(t *testing.T) {
	s := newTestEnv(t)
	makeBare(t, filepath.Join(s.bbRoot, "ws/vazio.git"))
	wantAction(t, s.checkedRepo("ws/vazio"), ActionSourceEmpty)
}

func TestVerificacaoProjetoVazioPreExistenteBloqueia(t *testing.T) {
	s := newTestEnv(t)
	s.gl.addProject(ns+"/repo", "none", "")
	repo := s.checkedRepo("ws/repo")
	wantAction(t, repo, ActionEmptyDest)
	if exitCode(nil, []*Repo{repo}) != 1 {
		t.Fatal("bloqueio devia sair com 1")
	}
}

func TestVerificacaoDestinoComAlgumCommitNaoEConcluido(t *testing.T) {
	s := newTestEnv(t)
	s.gitOnlyProject("refs/heads/main")
	repo := s.checkedRepo("ws/repo")
	wantAction(t, repo, ActionResume)
	if !reflect.DeepEqual(repo.MissingRefs, []string{"refs/heads/dev", "refs/tags/v1"}) {
		t.Fatalf("missing = %v", repo.MissingRefs)
	}
}

func TestVerificacaoRefsExtrasNoDestinoNaoImpedemConcluido(t *testing.T) {
	s := newTestEnv(t)
	p := s.gitOnlyProject()
	s.gl.copyRefs("ws/repo", p, "refs/heads/main:refs/heads/so-no-gitlab")
	wantAction(t, s.checkedRepo("ws/repo"), ActionDone)
}
