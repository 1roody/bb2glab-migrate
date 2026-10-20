package migration

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/1roody/bb2glab-migrate/internal/git"
	"github.com/1roody/bb2glab-migrate/internal/gitlab"
	"github.com/1roody/bb2glab-migrate/internal/terminal"
)

// checkRepo decide a ação de um repositório comparando as refs da origem e do destino
// (somente leitura).
func (m *Migration) checkRepo(repo *Repo) {
	state, proj, errMsg := m.gitlab.FindProject(repo.Dest)
	if state == gitlab.LookupFailed {
		repo.Action, repo.Reason = ActionUnverified, "GitLab: "+errMsg
		return
	}
	repo.Project = proj
	srcRefs, err := m.listRemoteRefs(m.bitbucket.CloneURL(repo.Source), m.bitbucket.GitAuthEnv())
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "could not read Username") { // o Git só pede usuário quando o Bitbucket recusa o header
			msg = "credenciais recusadas no Git (token inválido/expirado ou sem read:repository:bitbucket)"
		}
		repo.Action, repo.Reason = ActionUnverified, "Bitbucket: "+msg
		return
	}
	repo.SourceRefs = srcRefs
	if len(srcRefs) == 0 {
		repo.Action = ActionSourceEmpty
		return
	}
	reimport := func(action Action) {
		if problem := invalidProjectName(repo.Name()); problem != "" {
			repo.Action, repo.Reason = ActionUnverified, problem
		} else {
			repo.Action, repo.Reason = action, ""
		}
	}
	if state == gitlab.NotFound {
		reimport(ActionImport)
		return
	}
	if proj.ImportRunning() {
		repo.Action, repo.Reason = ActionImportRunning, fmt.Sprintf("import_status=%s; rode de novo quando terminar", proj.ImportStatus)
		return
	}

	// Os PRs só vêm pelo importador, que só cria projetos novos: projeto existente sem PRs só
	// recebe push Git com --allow-git-only; para trazer os PRs, --retry-failed-imports/--reimport.
	empty := proj.EmptyRepo
	switch {
	case proj.ImportStatus == "failed":
		if m.opts.RetryFailedImports && empty && proj.ImportType == "bitbucket" {
			reimport(ActionRetryImport)
			return
		}
		if m.opts.Reimport {
			reimport(ActionReimport)
			return
		}
		if !m.opts.AllowGitOnly {
			detail := proj.ImportError
			if detail == "" {
				detail = "sem detalhe"
			}
			hint := "use --reimport (arquiva e reimporta)"
			if empty {
				hint = "use --retry-failed-imports (apaga o vazio e reimporta)"
			}
			repo.Action, repo.Reason = ActionPreviousImportFailed, detail+" · "+hint+" ou --allow-git-only (sem PRs)"
			return
		}
	case !proj.ImportedWithPRs():
		if m.opts.Reimport {
			reimport(ActionReimport)
			return
		}
		if empty && !m.opts.AllowGitOnly {
			repo.Action = ActionEmptyDest
			repo.Reason = "apague o projeto para importar com PRs, ou use --reimport (arquiva e reimporta) ou --allow-git-only (sem PRs)"
			return
		}
	}
	dstRefs := git.Refs{}
	if !empty {
		if dstRefs, err = m.listRemoteRefs(proj.HTTPURLToRepo, m.gitlab.GitAuthEnv()); err != nil {
			repo.Action, repo.Reason = ActionUnverified, "GitLab (git ls-remote): "+err.Error()
			return
		}
	}
	repo.MissingRefs, repo.DivergentRefs = git.DiffRefs(repo.SourceRefs, dstRefs)
	switch {
	case len(repo.DivergentRefs) > 0 && m.opts.Force:
		repo.Action = ActionOverwrite
	case len(repo.DivergentRefs) > 0:
		repo.Action = ActionDivergent
	case len(repo.MissingRefs) > 0 && len(dstRefs) > 0:
		repo.Action = ActionResume
	case len(repo.MissingRefs) > 0:
		repo.Action = ActionPush
	default:
		repo.Action = ActionDone
	}
}

func (m *Migration) checkAll(repos []*Repo) {
	t0 := time.Now()
	total := len(repos)
	sp := m.ui.Spinner(fmt.Sprintf("comparando %d origem(ns) e destino(s)…", total), "  ")
	jobs := make(chan *Repo)
	var wg sync.WaitGroup
	var done atomic.Int32
	for range min(8, max(total, 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range jobs {
				m.checkRepo(e)
				sp.Set(fmt.Sprintf("comparando refs no Bitbucket e no GitLab… %d/%d", done.Add(1), total))
			}
		}()
	}
	for _, repo := range repos {
		jobs <- repo
	}
	close(jobs)
	wg.Wait()
	sp.Stop()
	m.ui.OK(fmt.Sprintf("%d repositório(s) verificados no Bitbucket e no GitLab em %s", total, terminal.Elapsed(t0)), "  ")

	// Projetos a criar: o grupo de destino precisa existir (o importador criaria grupos
	// faltantes ou importaria no namespace do usuário; aqui isso vira bloqueio).
	namespaces := map[string]bool{}
	for _, repo := range repos {
		if repo.Action == ActionImport {
			namespaces[repo.Namespace()] = true
		}
	}
	var sorted []string
	for ns := range namespaces {
		sorted = append(sorted, ns)
	}
	sort.Strings(sorted)
	for _, ns := range sorted {
		id, errMsg := m.gitlab.NamespaceID(ns)
		for _, repo := range repos {
			if repo.Action != ActionImport || repo.Namespace() != ns {
				continue
			}
			if id == 0 {
				repo.Action, repo.Reason = ActionUnverified, fmt.Sprintf("grupo de destino %s não encontrado no GitLab (%s)", ns, errMsg)
			} else {
				repo.NamespaceID = id
			}
		}
	}
}
