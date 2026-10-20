package migration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1roody/bb2glab-migrate/internal/git"
	"github.com/1roody/bb2glab-migrate/internal/gitlab"
)

// ---- importador (API)
func TestImportSucessoSemPushRedundante(t *testing.T) {
	s := newTestEnv(t)
	repo := s.checkedRepo("ws/repo")
	wantAction(t, repo, ActionImport)
	r := s.migrateRepo(repo, "")
	wantStatus(t, r, StatusComplete)
	if r.PushedRefs != 0 {
		t.Fatalf("pushed = %d", r.PushedRefs)
	}
	eqRefs(t, s.destRefs(), s.srcSHAs)
	if _, err := os.Stat(filepath.Join(s.workdir, "repo.git")); err == nil {
		t.Fatal("não devia ter clonado")
	}
}

func TestImportFalhaAntesDeCriarNaoCriaProjetoSemPRs(t *testing.T) {
	s := newTestEnv(t)
	s.gl.importMode = "fail_before"
	wantStatus(t, s.migrateRepo(s.checkedRepo("ws/repo"), ""), StatusFailed)
	if s.createdEmpty() != 0 || len(s.gl.projects) != 0 {
		t.Fatal("não devia criar projeto")
	}
}

func TestImportFalhaAntesDeCriarComAllowGitOnly(t *testing.T) {
	s := newTestEnv(t)
	s.gl.importMode = "fail_before"
	r := s.migrateRepo(s.checkedRepo("ws/repo"), "", allowGitOnly)
	wantStatus(t, r, StatusPartial)
	if !strings.Contains(r.ImportNote, "recusada") {
		t.Fatalf("api = %q", r.ImportNote)
	}
	if s.createdEmpty() != 1 {
		t.Fatalf("POST /projects = %d", s.createdEmpty())
	}
	eqRefs(t, s.destRefs(), s.srcSHAs)
}

func TestImportTentativaDuplicadaNaoImpedeFallback(t *testing.T) {
	s := newTestEnv(t)
	repo := s.checkedRepo("ws/repo")
	s.gl.addProject(ns+"/repo", "none", "") // alguém criou o projeto entre a verificação e a importação
	wantStatus(t, s.migrateRepo(repo, "", allowGitOnly), StatusPartial)
	if s.createdEmpty() != 0 {
		t.Fatal("devia reutilizar o projeto, não criar")
	}
	eqRefs(t, s.destRefs(), s.srcSHAs)
}

func TestImportFalhaAposCriarProjetoSemAllowEFalha(t *testing.T) {
	s := newTestEnv(t)
	s.gl.importMode = "fail_after"
	r := s.migrateRepo(s.checkedRepo("ws/repo"), "")
	wantStatus(t, r, StatusFailed)
	if !strings.Contains(r.ImportNote, "Bitbucket API: 500") {
		t.Fatalf("api = %q", r.ImportNote)
	}
	eqRefs(t, s.destRefs(), git.Refs{}) // nada enviado por Git: o projeto segue sem PRs só se pedido
	repo := s.checkedRepo("ws/repo")    // reexecução: bloqueado, com orientação
	wantAction(t, repo, ActionPreviousImportFailed)
	if !strings.Contains(repo.Reason, "--retry-failed-imports") {
		t.Fatalf("reason = %q", repo.Reason)
	}
}

func TestImportFalhaAposCriarProjetoComAllowReutilizaProjeto(t *testing.T) {
	s := newTestEnv(t)
	s.gl.importMode = "fail_after"
	wantStatus(t, s.migrateRepo(s.checkedRepo("ws/repo"), "", allowGitOnly), StatusPartial)
	if s.createdEmpty() != 0 {
		t.Fatal("devia reutilizar o projeto")
	}
	eqRefs(t, s.destRefs(), s.srcSHAs)
}

func TestImportPendenteTimeoutNaoFazPush(t *testing.T) {
	s := newTestEnv(t)
	s.gl.importMode = "pending"
	r := s.migrateRepo(s.checkedRepo("ws/repo"), "", noTimeout)
	wantStatus(t, r, StatusFailed)
	if !strings.Contains(r.ImportNote, "não terminou") {
		t.Fatalf("api = %q", r.ImportNote)
	}
	eqRefs(t, s.destRefs(), git.Refs{})
	// reexecução: a importação ainda rodando bloqueia o repositório, sem repetir o importador
	wantAction(t, s.checkedRepo("ws/repo"), ActionImportRunning)
}

func TestImportComRefsParciaisCompletaPorGit(t *testing.T) {
	s := newTestEnv(t)
	s.gl.importMode = "partial"
	r := s.migrateRepo(s.checkedRepo("ws/repo"), "")
	wantStatus(t, r, StatusPartial)
	if r.PushedRefs != 2 { // dev + v1
		t.Fatalf("pushed = %d", r.PushedRefs)
	}
	if !strings.Contains(r.MetadataNote, "Git precisou ser completado") {
		t.Fatalf("meta = %q", r.MetadataNote)
	}
	eqRefs(t, s.destRefs(), s.srcSHAs)
}

func TestImportRelacoesComFalhaNaoSaoSucessoCompleto(t *testing.T) {
	s := newTestEnv(t)
	s.gl.importMode = "failed_relations"
	r := s.migrateRepo(s.checkedRepo("ws/repo"), "")
	wantStatus(t, r, StatusPartial)
	if !strings.Contains(r.ImportNote, "1 relação") {
		t.Fatalf("api = %q", r.ImportNote)
	}
}

func TestImportCredenciaisInvalidasComAllowUsamSoFallback(t *testing.T) {
	s := newTestEnv(t)
	r := s.migrateRepo(s.checkedRepo("ws/repo"), "BITBUCKET_USER precisa ser o e-mail", allowGitOnly)
	wantStatus(t, r, StatusPartial)
	if s.called("POST", "import/bitbucket") {
		t.Fatal("não devia chamar o importador")
	}
	eqRefs(t, s.destRefs(), s.srcSHAs)
}

func TestImportReexecucaoAposSucessoEConcluido(t *testing.T) {
	s := newTestEnv(t)
	s.migrateRepo(s.checkedRepo("ws/repo"), "")
	s.gl.calls = nil
	wantAction(t, s.checkedRepo("ws/repo"), ActionDone)
	if s.called("POST", "import/bitbucket") {
		t.Fatal("não devia chamar o importador de novo")
	}
}

// ---- --retry-failed-imports
func failedImport(s *testEnv) {
	s.gl.importMode = "fail_after"
	s.migrateRepo(s.checkedRepo("ws/repo"), "")
	s.gl.importMode = "success"
}

func TestRetryApagaOVazioEReimporta(t *testing.T) {
	s := newTestEnv(t)
	failedImport(s)
	repo := s.checkedRepo("ws/repo", retryFailedImports)
	wantAction(t, repo, ActionRetryImport)
	wantStatus(t, s.migrateRepo(repo, ""), StatusComplete)
	eqRefs(t, s.destRefs(), s.srcSHAs)
}

func TestRetryExclusaoAdiadaUsaRemocaoDefinitiva(t *testing.T) {
	s := newTestEnv(t)
	failedImport(s)
	s.gl.delayedDelete = true
	wantStatus(t, s.migrateRepo(s.checkedRepo("ws/repo", retryFailedImports), ""), StatusComplete)
	found := false
	for _, c := range s.gl.calls {
		found = found || strings.Contains(c.Endpoint, "permanently_remove=true")
	}
	if !found {
		t.Fatal("devia pedir a remoção definitiva")
	}
}

func TestRetryNaoApagaProjetoQueNaoEImportacaoFalha(t *testing.T) {
	s := newTestEnv(t)
	s.gitOnlyProject()
	wantAction(t, s.checkedRepo("ws/repo", retryFailedImports), ActionDone) // não toca em projeto comum
	for _, c := range s.gl.calls {
		if c.Method == "DELETE" {
			t.Fatal("não devia apagar nada")
		}
	}
}

// ---- --reimport
func TestReimportProjetoSoGitConcluidoSugereReimport(t *testing.T) {
	s := newTestEnv(t)
	s.gitOnlyProject()
	repo := s.checkedRepo("ws/repo")
	wantAction(t, repo, ActionDone)
	if repo.Project.ImportedWithPRs() {
		t.Fatal("projeto só Git não foi importado com PRs")
	}
}

func TestReimportPreservaOTrabalhoFeitoNoGitLab(t *testing.T) {
	s := newTestEnv(t)
	old := s.gitOnlyProject()
	// Trabalho feito no GitLab depois da 1ª migração: avanço em main, branch nova, dev divergente.
	other := filepath.Join(s.work, "gl-work")
	gitT(t, "", "clone", "-q", old.HTTPURL, other)
	gitT(t, other, "checkout", "-q", "main")
	mainGL := commitT(t, other, "main no gitlab")
	gitT(t, other, "checkout", "-q", "-b", "so-no-gitlab")
	commitT(t, other, "branch nova")
	gitT(t, other, "checkout", "-q", "dev")
	gitT(t, other, "reset", "-q", "--hard", "main~1")
	devGL := commitT(t, other, "dev reescrito no gitlab")
	gitT(t, other, "push", "-q", "--force", "origin", "main", "so-no-gitlab", "dev")

	repo := s.checkedRepo("ws/repo", reimport)
	wantAction(t, repo, ActionReimport)
	r := s.migrateRepo(repo, "")
	wantStatus(t, r, StatusComplete)

	cur := s.gl.projects[ns+"/repo"]
	if cur.ID == old.ID || cur.ImportType != "bitbucket" {
		t.Fatalf("projeto novo não foi importado: %+v", cur)
	}
	if want := ns + "/repo-pre-reimport-" + itoa(old.ID); old.PathNS != want || !old.Archived {
		t.Fatalf("projeto anterior: %s archived=%t", old.PathNS, old.Archived)
	}
	refs := s.destRefs()
	if refs["refs/heads/main"] != mainGL {
		t.Fatal("avanço do GitLab em main (fast-forward) não foi reenviado")
	}
	if _, ok := refs["refs/heads/so-no-gitlab"]; !ok {
		t.Fatal("branch só do GitLab não foi reenviada")
	}
	if refs["refs/heads/dev"] != s.srcSHAs["refs/heads/dev"] {
		t.Fatal("dev devia ser o da origem")
	}
	if refs["refs/heads/gitlab-pre-reimport/dev"] != devGL {
		t.Fatal("divergência de dev não foi preservada")
	}
	if !strings.Contains(strings.Join(r.Warnings, "\n"), "gitlab-pre-reimport/dev") {
		t.Fatalf("warnings = %v", r.Warnings)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestReimportRecusadoDesfazORename(t *testing.T) {
	s := newTestEnv(t)
	old := s.gitOnlyProject()
	s.gl.importMode = "fail_before"
	wantStatus(t, s.migrateRepo(s.checkedRepo("ws/repo", reimport), ""), StatusFailed)
	if old.PathNS != ns+"/repo" || old.Archived || s.gl.projects[ns+"/repo"].ID != old.ID {
		t.Fatalf("rename não foi desfeito: %+v", old)
	}
}

func TestReimportIgnoraProjetoJaImportadoComPRs(t *testing.T) {
	s := newTestEnv(t)
	s.migrateRepo(s.checkedRepo("ws/repo"), "")
	wantAction(t, s.checkedRepo("ws/repo", reimport), ActionDone)
}

func TestCaminhoRedirecionadoContaComoLivre(t *testing.T) {
	s := newTestEnv(t)
	old := s.gitOnlyProject()
	gl := &gitlab.Client{API: s.gl, Host: gitlab.DefaultHost}
	if _, err := gl.RenameProject(gitlab.Project{ID: old.ID}, "outro", "outro"); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := gl.FindProject(ns + "/repo"); state != gitlab.NotFound {
		t.Fatalf("state = %v", state)
	}
}

// ---- fallback Git
func TestFallbackProjetoVazioComAllowPushCompletoEBranchPadrao(t *testing.T) {
	s := newTestEnv(t)
	s.gl.addProject(ns+"/repo", "none", "")
	repo := s.checkedRepo("ws/repo", allowGitOnly)
	wantAction(t, repo, ActionPush)
	r := s.migrateRepo(repo, "", allowGitOnly)
	wantStatus(t, r, StatusPartial)
	if r.MetadataNote != "não migrados" {
		t.Fatalf("meta = %q", r.MetadataNote)
	}
	eqRefs(t, s.destRefs(), s.srcSHAs)
	if !s.called("PUT", "projects/"+itoa(s.gl.projects[ns+"/repo"].ID)) {
		t.Fatal("devia definir a branch padrão")
	}
}

func TestFallbackRetomadaEnviaSoOQueFaltaEPreservaExtras(t *testing.T) {
	s := newTestEnv(t)
	s.gitOnlyProject("refs/heads/main", "refs/heads/main:refs/heads/extra")
	r := s.migrateRepo(s.checkedRepo("ws/repo"), "")
	wantStatus(t, r, StatusPartial)
	if r.PushedRefs != 2 {
		t.Fatalf("pushed = %d", r.PushedRefs)
	}
	refs := s.destRefs()
	if _, ok := refs["refs/heads/extra"]; !ok {
		t.Fatal("ref extra do destino foi perdida")
	}
	delete(refs, "refs/heads/extra")
	eqRefs(t, refs, s.srcSHAs)
}

func divergentProject(s *testEnv) string {
	p := s.gitOnlyProject()
	other := filepath.Join(s.work, "outro")
	gitT(s.t, "", "clone", "-q", "-b", "main", p.HTTPURL, other)
	sha := commitT(s.t, other, "só no gitlab")
	gitT(s.t, other, "push", "-q", "origin", "HEAD:refs/heads/main")
	return sha
}

func TestFallbackDivergenciaBloqueiaSemForce(t *testing.T) {
	s := newTestEnv(t)
	glSHA := divergentProject(s)
	repo := s.checkedRepo("ws/repo")
	wantAction(t, repo, ActionDivergent)
	if repo.DivergentRefs[0].Ref != "refs/heads/main" || exitCode(nil, []*Repo{repo}) != 1 {
		t.Fatalf("divergent = %v", repo.DivergentRefs)
	}
	// mesmo se chegar à migração (destino mudou após a verificação), nada é sobrescrito
	repo.Action = ActionResume
	wantStatus(t, s.migrateRepo(repo, ""), StatusFailed)
	if s.destRefs()["refs/heads/main"] != glSHA {
		t.Fatal("main do destino foi sobrescrita")
	}
}

func TestFallbackForceSobrescreveSoAsDivergentes(t *testing.T) {
	s := newTestEnv(t)
	divergentProject(s)
	repo := s.checkedRepo("ws/repo", force)
	wantAction(t, repo, ActionOverwrite)
	r := s.migrateRepo(repo, "", force)
	wantStatus(t, r, StatusPartial)
	if r.PushedRefs != 1 {
		t.Fatalf("pushed = %d", r.PushedRefs)
	}
	eqRefs(t, s.destRefs(), s.srcSHAs)
}

func TestFallbackVerificacaoFinalComErroEFalha(t *testing.T) {
	s := newTestEnv(t)
	s.gitOnlyProject("refs/heads/main")
	repo := s.checkedRepo("ws/repo")
	n := 0
	s.listRemoteRefs = func(u string, env []string) (git.Refs, error) {
		if strings.HasPrefix(u, s.gl.root) {
			n++
			if n == 2 { // 1ª: antes do push; 2ª: verificação final
				return nil, errors.New("fatal: unable to access")
			}
		}
		return git.ListRemoteRefs(u, env)
	}
	r := s.migrateRepo(repo, "")
	wantStatus(t, r, StatusFailed)
	if !strings.Contains(r.GitNote, "verificação final") {
		t.Fatalf("git = %q", r.GitNote)
	}
}
