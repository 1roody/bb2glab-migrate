package migration

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/1roody/bb2glab-migrate/internal/git"
	"github.com/1roody/bb2glab-migrate/internal/gitlab"
)

// deleteFailedImport (--retry-failed-imports) apaga o projeto vazio deixado por uma
// importação que falhou e confirma que o caminho ficou livre. Só apaga se ainda estiver vazio
// e com importação falha.
func (m *Migration) deleteFailedImport(repo *Repo) bool {
	ui := m.ui
	state, proj, _ := m.gitlab.FindProject(repo.Dest)
	if state != gitlab.Found || proj.ImportType != "bitbucket" || proj.ImportStatus != "failed" || !proj.EmptyRepo {
		ui.Fail("o destino não é mais um projeto vazio de importação falha; nada foi apagado", stepIndent)
		return false
	}
	if err := m.gitlab.Request(http.MethodDelete, fmt.Sprintf("projects/%d", proj.ID), nil, nil); err != nil {
		ui.Fail(fmt.Sprintf("não foi possível apagar o projeto: %s (requer papel Owner)", gitlab.ErrorText(err)), stepIndent)
		return false
	}
	if state, _, _ := m.gitlab.FindProject(repo.Dest); state != gitlab.NotFound {
		// O gitlab.com pode só marcar para exclusão (exclusão adiada): tenta a remoção definitiva.
		full := repo.Dest
		var cur gitlab.Project
		if err := m.gitlab.Request(http.MethodGet, fmt.Sprintf("projects/%d", proj.ID), nil, &cur); err == nil && cur.PathWithNamespace != "" {
			full = cur.PathWithNamespace
		}
		m.gitlab.Request(http.MethodDelete, fmt.Sprintf("projects/%d?permanently_remove=true&full_path=%s", proj.ID, gitlab.EscapePath(full)), nil, nil)
		if state, _, _ := m.gitlab.FindProject(repo.Dest); state != gitlab.NotFound {
			ui.Fail("projeto marcado para exclusão, mas o caminho continua ocupado; tente de novo mais tarde", stepIndent)
			return false
		}
	}
	ui.OK(fmt.Sprintf("projeto vazio da importação que falhou apagado (id %d)", proj.ID), stepIndent)
	return true
}

func (m *Migration) undoRename(old gitlab.Project) {
	m.gitlab.Request(http.MethodPost, fmt.Sprintf("projects/%d/unarchive", old.ID), nil, nil)
	if _, err := m.gitlab.RenameProject(old, old.Path, old.Name); err == nil {
		m.ui.OK(fmt.Sprintf("rename desfeito: o projeto voltou para %s (desarquivado)", old.PathWithNamespace), stepIndent)
	} else {
		m.ui.Fail(fmt.Sprintf("não foi possível desfazer o rename (%s): corrija manualmente o projeto id %d", gitlab.ErrorText(err), old.ID), stepIndent)
	}
}

// reimportProject (--reimport) renomeia e arquiva o projeto atual e importa com PRs no
// caminho original. Se a importação nem chegar a criar o projeto novo, desfaz o rename e o
// arquivamento.
func (m *Migration) reimportProject(repo *Repo, res *Result, problem string) *gitlab.Project {
	ui := m.ui
	state, old, errMsg := m.gitlab.FindProject(repo.Dest)
	if state != gitlab.Found {
		what := "não existe mais"
		if state == gitlab.LookupFailed {
			what = "não verificado: " + errMsg
		}
		ui.Fail("projeto atual "+what, stepIndent)
		return nil
	}
	if old.ImportRunning() {
		ui.Fail(fmt.Sprintf("importação em andamento no destino (import_status=%s)", old.ImportStatus), stepIndent)
		return nil
	}
	backup := fmt.Sprintf("%s-pre-reimport-%d", repo.Name(), old.ID)
	renamed, err := m.gitlab.RenameProject(old, backup, backup)
	if err != nil {
		ui.Fail("não foi possível renomear o projeto atual: "+gitlab.ErrorText(err), stepIndent)
		return nil
	}
	archiveErr := m.gitlab.Request(http.MethodPost, fmt.Sprintf("projects/%d/archive", old.ID), nil, nil)
	if renamed.HTTPURLToRepo != "" {
		res.ArchivedProject = renamed
	} else {
		res.ArchivedProject = old
		res.ArchivedProject.PathWithNamespace = repo.Namespace() + "/" + backup
	}
	msg := "projeto atual renomeado para " + repo.Namespace() + "/" + backup
	if archiveErr == nil {
		msg += " e arquivado"
	}
	ui.OK(msg, stepIndent)
	if archiveErr != nil {
		res.addWarning(fmt.Sprintf("não foi possível arquivar %s/%s: %s", repo.Namespace(), backup, gitlab.ErrorText(archiveErr)))
		ui.Warn(res.Warnings[len(res.Warnings)-1], stepIndent)
	}
	if state, _, _ := m.gitlab.FindProject(repo.Dest); state != gitlab.NotFound {
		ui.Fail("o caminho original continua ocupado depois do rename", stepIndent)
		m.undoRename(old)
		res.ArchivedProject = gitlab.Project{}
		return nil
	}

	proj := m.importProject(repo, res, problem, false)
	if proj == nil {
		if state, _, _ := m.gitlab.FindProject(repo.Dest); state == gitlab.NotFound {
			m.undoRename(old)
			res.ArchivedProject = gitlab.Project{}
		} else {
			ui.Warn(fmt.Sprintf("o projeto anterior continua arquivado em %s/%s", repo.Namespace(), backup), stepIndent)
		}
		return nil
	}
	return proj
}

// restoreGitLabOnlyWork (--reimport) reenvia ao projeto novo o que só existia no projeto
// anterior (trabalho feito no GitLab depois da primeira migração). Nada é sobrescrito:
// avanços viram fast-forward e divergências são preservadas como gitlab-pre-reimport/<ref>.
func (m *Migration) restoreGitLabOnlyWork(old, cur gitlab.Project, workdir string, res *Result) {
	ui := m.ui
	backup := old.PathWithNamespace
	if backup == "" {
		backup = "?"
	}
	warn := func(msg string) {
		res.addWarning(msg)
		ui.Warn(msg, stepIndent)
	}

	oldRefs, err := m.listRemoteRefs(old.HTTPURLToRepo, m.gitlab.GitAuthEnv())
	newRefs, err2 := m.listRemoteRefs(cur.HTTPURLToRepo, m.gitlab.GitAuthEnv())
	if err == nil {
		err = err2
	}
	if err != nil {
		warn(fmt.Sprintf("não foi possível comparar com o projeto anterior (%v): confira %s manualmente", err, backup))
		return
	}
	diff := git.Refs{}
	for ref, sha := range oldRefs {
		if newRefs[ref] != sha {
			diff[ref] = sha
		}
	}
	if len(diff) == 0 {
		ui.OK("nenhum commit feito só no GitLab para reenviar", stepIndent)
		return
	}

	repo := filepath.Join(workdir, "anterior.git")
	os.RemoveAll(repo)
	defer os.RemoveAll(repo)
	sp := ui.Spinner("comparando com o projeto anterior…", stepIndent)
	ok, out := m.runGitLogged("", m.gitlab.GitAuthEnv(), "clone", "--mirror", old.HTTPURLToRepo, repo)
	if ok {
		ok, out = m.runGitLogged(repo, m.gitlab.GitAuthEnv(), "fetch", cur.HTTPURLToRepo,
			"+refs/heads/*:refs/reimport-novo/heads/*", "+refs/tags/*:refs/reimport-novo/tags/*")
	}
	sp.Stop()
	if !ok {
		ui.PrintOutputTail(out, stepIndent+"  ")
		warn(fmt.Sprintf("não foi possível ler o projeto anterior: confira %s manualmente", backup))
		return
	}

	contains := func(a, b string) bool { return git.Run(repo, nil, "merge-base", "--is-ancestor", a, b).OK() }
	type preserved struct{ ref, target string }
	var specs []string
	var kept []preserved
	for _, ref := range git.SortedNames(diff) {
		sha := diff[ref]
		cur, exists := newRefs[ref]
		if exists && contains(sha, cur) {
			continue // o projeto novo já contém esse commit
		}
		if !exists || contains(cur, sha) {
			specs = append(specs, ref+":"+ref) // ref só no GitLab, ou avanço feito no GitLab (fast-forward)
			continue
		}
		target := ref
		if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			target = "refs/heads/gitlab-pre-reimport/" + name
		} else if name, ok := strings.CutPrefix(ref, "refs/tags/"); ok {
			target = "refs/tags/gitlab-pre-reimport/" + name
		}
		specs = append(specs, ref+":"+target)
		kept = append(kept, preserved{ref, target})
	}
	if len(specs) == 0 {
		ui.OK("o projeto novo já contém tudo o que havia no anterior", stepIndent)
		return
	}
	if ok, out := m.pushRefspecs(repo, cur.HTTPURLToRepo, specs); !ok {
		ui.PrintOutputTail(out, stepIndent+"  ")
		warn(fmt.Sprintf("falha ao reenviar o trabalho feito só no GitLab (detalhes no log): confira %s", backup))
		return
	}
	ui.OK(fmt.Sprintf("%d ref(s) com trabalho feito só no GitLab reenviadas ao projeto novo", len(specs)), stepIndent)
	for _, p := range kept {
		warn(fmt.Sprintf("%s divergia da origem: preservada como %s", git.RefLabel(p.ref), git.RefLabel(p.target)))
	}
}
