package migration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/1roody/bb2glab-migrate/internal/git"
	"github.com/1roody/bb2glab-migrate/internal/gitlab"
	"github.com/1roody/bb2glab-migrate/internal/terminal"
)

// runGitLogged roda o git; a saída bruta vai só para o log e é devolvida para exibir em falhas.
func (m *Migration) runGitLogged(dir string, env []string, args ...string) (bool, string) {
	p := git.Run(dir, env, args...)
	output := p.Stdout + p.Stderr
	m.ui.LogCommand(append([]string{"git"}, args...), output)
	return p.OK(), output
}

// pushRefspecs faz push em lotes de 100 refspecs. ci.skip: o push não cria pipelines (senão
// cada branch dispara o .gitlab-ci.yml).
func (m *Migration) pushRefspecs(repo, url string, specs []string) (bool, string) {
	var output strings.Builder
	for i := 0; i < len(specs); i += 100 {
		args := append([]string{"push", "-o", "ci.skip", url}, specs[i:min(i+100, len(specs))]...)
		ok, out := m.runGitLogged(repo, m.gitlab.GitAuthEnv(), args...)
		output.WriteString(out)
		if !ok {
			return false, output.String()
		}
	}
	return true, output.String()
}

// pushRefs envia só as refs pedidas; '+' (sobrescrita) apenas nas de forceRefs.
func (m *Migration) pushRefs(mirror, url string, refs []string, forceRefs map[string]bool) (bool, string) {
	specs := make([]string, 0, len(refs))
	for _, r := range refs {
		if forceRefs[r] {
			specs = append(specs, "+"+r+":"+r)
		} else {
			specs = append(specs, r+":"+r)
		}
	}
	return m.pushRefspecs(mirror, url, specs)
}

func (m *Migration) printRepoHeader(i, n int, repo *Repo) {
	ui := m.ui
	ui.Print("")
	ui.Print(fmt.Sprintf("%s %s  %s", ui.Bold(fmt.Sprintf("[%d/%d]", i, n)), ui.Bold(repo.Source), ui.Dim("("+string(repo.Action)+")")))
	ui.Print(stepIndent + ui.Dim("→ "+m.gitlab.Host+"/"+repo.Dest))
}

// waitForImport acompanha a importação assíncrona até "finished", "failed", "timeout" ou
// "interrupted" (Ctrl+C).
func (m *Migration) waitForImport(projectID int) (string, gitlab.ImportInfo) {
	t0 := time.Now()
	var info gitlab.ImportInfo
	last := ""
	sp := m.ui.Spinner("importando pelo GitLab…", stepIndent)
	defer sp.Stop()
	for {
		var cur gitlab.ImportInfo
		if err := m.gitlab.Request(http.MethodGet, fmt.Sprintf("projects/%d/import", projectID), nil, &cur); err == nil {
			info = cur
		}
		status := info.ImportStatus
		if status == "" {
			status = "?"
		}
		if status != last {
			m.ui.Log(fmt.Sprintf("    import_status=%s (%s)", status, terminal.Elapsed(t0)))
			last = status
		}
		if status == "finished" || status == "failed" {
			return status, info
		}
		if time.Since(t0) >= time.Duration(m.opts.ImportTimeout)*time.Second {
			return "timeout", info
		}
		sp.Set(fmt.Sprintf("importando pelo GitLab… import_status=%s (%s)", status, terminal.Elapsed(t0)))
		select {
		case <-m.ctx.Done():
			return "interrupted", info
		case <-time.After(m.deps.PollInterval):
		}
	}
}

// importProject usa o importador oficial e devolve o projeto para a verificação/complemento
// Git (nil = falha). Sem allowFallback (--allow-git-only) nunca deixa um projeto sem PRs: se
// a importação for recusada ou falhar, o repositório termina como falha.
func (m *Migration) importProject(repo *Repo, res *Result, problem string, allowFallback bool) *gitlab.Project {
	ui := m.ui
	var proj *gitlab.Project
	if problem != "" { // só chega aqui com --allow-git-only: run() para antes, sem a opção
		res.ImportNote = "não usado: " + problem
		ui.Warn("importador não usado: "+problem, stepIndent)
	} else {
		body := map[string]any{"bitbucket_email": m.bitbucket.User, "bitbucket_api_token": m.bitbucket.Token,
			"repo_path": repo.Source, "target_namespace": repo.Namespace(), "new_name": repo.Name()}
		sp := ui.Spinner("solicitando importação ao GitLab…", stepIndent)
		raw, err := m.gitlab.API.Do(http.MethodPost, "import/bitbucket", body)
		sp.Stop()
		verdict := "aceito"
		if err != nil {
			verdict = gitlab.ErrorText(err)
		}
		ui.Log(fmt.Sprintf("    POST import/bitbucket repo_path=%s target_namespace=%s new_name=%s -> %s",
			repo.Source, repo.Namespace(), repo.Name(), verdict))
		if err != nil {
			res.ImportNote = "recusada: " + gitlab.ErrorText(err)
			if !allowFallback {
				state, _, _ := m.gitlab.FindProject(repo.Dest)
				ui.Fail("importação "+res.ImportNote, stepIndent)
				hint := "nada foi criado: corrija o motivo e rode de novo"
				if state == gitlab.Found {
					hint = "o projeto ficou criado vazio: rode de novo com --retry-failed-imports"
				}
				ui.Print(stepIndent + "  " + ui.Dim(hint))
				return nil
			}
			ui.Warn("importador recusou: "+gitlab.ErrorText(err)+"; seguindo com o fallback Git (--allow-git-only)", stepIndent)
		} else {
			state, p, _ := m.gitlab.FindProject(repo.Dest)
			if state != gitlab.Found {
				var data struct {
					FullPath string `json:"full_path"`
					PathNS   string `json:"path_with_namespace"`
				}
				json.Unmarshal(raw, &data)
				found := data.FullPath
				if found == "" {
					found = data.PathNS
				}
				if found == "" {
					found = "?"
				}
				res.ImportNote = fmt.Sprintf("importação aceita, mas o projeto não apareceu em %s (GitLab informou: %s)", repo.Dest, found)
				ui.Fail(res.ImportNote, stepIndent)
				return nil
			}
			proj = &p
			ui.OK(fmt.Sprintf("importação aceita (projeto id %d)", p.ID), stepIndent)
			t := time.Now()
			status, info := m.waitForImport(p.ID)
			switch status {
			case "finished":
				failed := info.FailedRelations
				res.ImportOK = len(failed) == 0
				res.ImportNote = "concluída"
				if len(failed) > 0 {
					res.ImportNote += fmt.Sprintf(" com %d relação(ões) com falha", len(failed))
					ui.Warn(fmt.Sprintf("importação concluída com %d relação(ões) com falha (detalhes no log)", len(failed)), stepIndent)
					dump, _ := json.MarshalIndent(failed, "", "  ")
					ui.Log(string(dump))
				} else {
					ui.OK("importação concluída "+ui.Dim(terminal.Elapsed(t)), stepIndent)
				}
			case "failed":
				detail := info.ImportError
				if detail == "" {
					detail = "?"
				}
				res.ImportNote = "falhou: " + detail
				if !allowFallback {
					ui.Fail("importação "+res.ImportNote, stepIndent)
					ui.Print(stepIndent + "  " + ui.Dim("rode de novo com --retry-failed-imports (ou --allow-git-only, sem PRs)"))
					return nil
				}
				ui.Warn("importação "+res.ImportNote+"; seguindo com o fallback Git (--allow-git-only)", stepIndent)
			default:
				// Não envia por Git enquanto o importador ainda roda: os dois disputariam as refs.
				st := info.ImportStatus
				if st == "" {
					st = "?"
				}
				if status == "interrupted" {
					res.ImportNote = fmt.Sprintf("acompanhamento interrompido (import_status=%s)", st)
				} else {
					res.ImportNote = fmt.Sprintf("não terminou em %ds (import_status=%s)", m.opts.ImportTimeout, st)
				}
				ui.Fail("importação "+res.ImportNote+"; nada enviado por Git, rode de novo mais tarde", stepIndent)
				return nil
			}
		}
	}

	if proj == nil {
		// A tentativa de importação pode ter criado o projeto mesmo falhando: reutiliza; senão cria vazio.
		state, p, errMsg := m.gitlab.FindProject(repo.Dest)
		switch state {
		case gitlab.Found:
			ui.OK(fmt.Sprintf("projeto criado pela tentativa de importação (id %d) reutilizado", p.ID), stepIndent)
		case gitlab.NotFound:
			err := m.gitlab.Request(http.MethodPost, "projects", map[string]any{
				"name": repo.Name(), "path": repo.Name(), "namespace_id": repo.NamespaceID, "visibility": "private"}, &p)
			if err == nil && p.ID == 0 {
				err = &gitlab.APIError{Message: "resposta sem id do projeto"}
			}
			if err != nil {
				ui.Fail("não foi possível criar o projeto vazio: "+gitlab.ErrorText(err), stepIndent)
				return nil
			}
			ui.OK(fmt.Sprintf("projeto vazio criado (id %d, privado)", p.ID), stepIndent)
		default:
			ui.Fail("não foi possível consultar o destino no GitLab: "+errMsg, stepIndent)
			return nil
		}
		proj = &p
	}
	repo.Project = *proj
	return proj
}

// syncRefsByGit compara as refs, envia por Git só o que falta (sobrescreve divergentes apenas com
// --force) e faz a verificação final. true = todas as refs da origem conferidas no destino.
func (m *Migration) syncRefsByGit(repo *Repo, proj *gitlab.Project, workdir string, res *Result) bool {
	ui := m.ui
	url := proj.HTTPURLToRepo
	fail := func(msg, output string) bool {
		ui.Fail(msg, stepIndent)
		if strings.TrimSpace(output) != "" {
			ui.PrintOutputTail(output, stepIndent+"  ")
		}
		res.GitNote = msg
		return false
	}

	dstRefs, err := m.listRemoteRefs(url, m.gitlab.GitAuthEnv())
	if err != nil {
		return fail("não foi possível consultar as refs do destino: "+err.Error(), "")
	}
	missing, divergent := git.DiffRefs(repo.SourceRefs, dstRefs)
	if len(missing) == 0 && len(divergent) == 0 {
		ui.OK(fmt.Sprintf("destino já tem todas as %d ref(s) da origem; nenhum push necessário", len(repo.SourceRefs)), stepIndent)
		res.GitNote = "sem push (refs já completas)"
		return true
	}

	mirror := filepath.Join(workdir, "repo.git")
	os.RemoveAll(mirror)
	t := time.Now()
	sp := ui.Spinner("clonando (mirror) do Bitbucket…", stepIndent)
	ok, out := m.runGitLogged("", m.bitbucket.GitAuthEnv(), "clone", "--mirror", m.bitbucket.CloneURL(repo.Source), mirror)
	sp.Stop()
	if !ok {
		return fail("falha ao clonar "+repo.Source, out)
	}
	defer os.RemoveAll(mirror)
	repo.SourceRefs = git.ListLocalRefs(mirror) // o clone é a referência (a origem pode ter mudado desde a verificação)
	heads := 0
	for ref := range repo.SourceRefs {
		if strings.HasPrefix(ref, "refs/heads/") {
			heads++
		}
	}
	ui.OK(fmt.Sprintf("clone: %d branch(es), %d tag(s) %s", heads, len(repo.SourceRefs)-heads, ui.Dim(terminal.Elapsed(t))), stepIndent)

	if git.Run(mirror, nil, "grep", "-qs", "filter=lfs", "HEAD", "--", ".gitattributes").OK() {
		res.addWarning("usa Git LFS: o push Git não copia objetos LFS")
		ui.Warn(res.Warnings[len(res.Warnings)-1], stepIndent)
	}
	if git.Run(mirror, nil, "cat-file", "-e", "HEAD:bitbucket-pipelines.yml").OK() {
		res.addWarning("tem bitbucket-pipelines.yml: o CI precisa ser convertido para .gitlab-ci.yml")
		ui.Warn(res.Warnings[len(res.Warnings)-1], stepIndent)
	}

	missing, divergent = git.DiffRefs(repo.SourceRefs, dstRefs)
	if len(divergent) > 0 && !m.opts.Force {
		ui.Fail(fmt.Sprintf("%d ref(s) com SHA diferente no destino; nada foi sobrescrito (use --force)", len(divergent)), stepIndent)
		printDivergence(ui, divergent, stepIndent+"    ")
		res.GitNote = "bloqueado: refs divergentes"
		return false
	}
	forceRefs := map[string]bool{}
	var forced []string
	for _, d := range divergent {
		forceRefs[d.Ref] = true
		forced = append(forced, d.Ref)
	}
	sort.Strings(forced)
	t = time.Now()
	sp = ui.Spinner(fmt.Sprintf("enviando %d ref(s)…", len(missing)+len(forced)), stepIndent)
	ok, out = m.pushRefs(mirror, url, append(append([]string{}, missing...), forced...), forceRefs)
	sp.Stop()
	if !ok {
		return fail("push falhou", out)
	}
	res.PushedRefs = len(missing) + len(forced)
	msg := fmt.Sprintf("push: %d ref(s) nova(s)", len(missing))
	if len(forced) > 0 {
		msg += ui.Red(fmt.Sprintf(", %d sobrescrita(s) com --force", len(forced)))
	}
	ui.OK(msg+" "+ui.Dim(terminal.Elapsed(t)), stepIndent)

	if len(dstRefs) == 0 { // destino estava vazio: ajusta a branch padrão igual à do Bitbucket
		head := strings.TrimSpace(git.Run(mirror, nil, "symbolic-ref", "--short", "HEAD").Stdout)
		if _, ok := repo.SourceRefs["refs/heads/"+head]; head != "" && ok {
			if err := m.gitlab.Request(http.MethodPut, fmt.Sprintf("projects/%d", proj.ID), map[string]any{"default_branch": head}, nil); err == nil {
				ui.OK("branch padrão: "+head, stepIndent)
			} else {
				res.addWarning(fmt.Sprintf("não foi possível definir a branch padrão '%s'", head))
				ui.Warn(res.Warnings[len(res.Warnings)-1], stepIndent)
			}
		}
	}

	// A verificação final determina o resultado: sem conferir o destino, não há sucesso.
	final, err := m.listRemoteRefs(url, m.gitlab.GitAuthEnv())
	if err != nil {
		return fail("verificação final: não foi possível consultar o destino: "+err.Error(), "")
	}
	missing, divergent = git.DiffRefs(repo.SourceRefs, final)
	if len(missing) > 0 || len(divergent) > 0 {
		ui.Fail(fmt.Sprintf("verificação final: %d ref(s) ausente(s), %d divergente(s)", len(missing), len(divergent)), stepIndent)
		printDivergence(ui, divergent, stepIndent+"    ")
		for _, ref := range missing[:min(5, len(missing))] {
			ui.Print(stepIndent + "    " + ui.Dim(git.RefLabel(ref)+": ausente no destino"))
		}
		res.GitNote = "verificação final falhou"
		return false
	}
	extra := 0
	for ref := range final {
		if _, ok := repo.SourceRefs[ref]; !ok {
			extra++
		}
	}
	msg = fmt.Sprintf("verificação: todas as %d ref(s) da origem estão no destino", len(repo.SourceRefs))
	if extra > 0 {
		msg += ui.Dim(fmt.Sprintf(" (%d ref(s) só no destino)", extra))
	}
	ui.OK(msg, stepIndent)
	res.GitNote = fmt.Sprintf("%d ref(s) enviadas e verificadas", res.PushedRefs)
	return true
}

// migrateRepo executa a ação decidida na verificação para um repositório.
func (m *Migration) migrateRepo(repo *Repo, workdir, problem string) *Result {
	ui := m.ui
	res := &Result{Repo: repo, Status: StatusFailed}
	t0 := time.Now()
	imported := repo.Action.UsesImporter()
	var proj *gitlab.Project
	switch repo.Action {
	case ActionImport:
		proj = m.importProject(repo, res, problem, m.opts.AllowGitOnly)
	case ActionRetryImport:
		if m.deleteFailedImport(repo) {
			proj = m.importProject(repo, res, problem, false)
		}
	case ActionReimport:
		proj = m.reimportProject(repo, res, problem)
	default:
		res.ImportNote = "não usado: o projeto já existia (o importador do GitLab só cria projetos novos)"
		// Reexecução segura: confere de novo o destino logo antes de enviar.
		state, p, errMsg := m.gitlab.FindProject(repo.Dest)
		switch {
		case state == gitlab.NotFound:
			ui.Fail("destino não existe mais", stepIndent)
		case state == gitlab.LookupFailed:
			ui.Fail("destino não verificado: "+errMsg, stepIndent)
		case p.ImportRunning():
			ui.Fail(fmt.Sprintf("importação em andamento no destino (import_status=%s)", p.ImportStatus), stepIndent)
		default:
			ui.OK(fmt.Sprintf("destino encontrado (id %d)", p.ID), stepIndent)
			proj = &p
		}
	}

	switch {
	case proj == nil || !m.syncRefsByGit(repo, proj, workdir, res):
		res.Status = StatusFailed
	case res.ImportOK && res.PushedRefs == 0:
		res.Status, res.MetadataNote = StatusComplete, "trazidos pelo importador do GitLab"
	case !imported && res.PushedRefs == 0:
		res.Status = StatusSkipped // ficou completo entre a verificação e agora
	default:
		res.Status = StatusPartial
		switch {
		case res.ImportOK:
			res.MetadataNote = "não garantidos: o importador concluiu, mas o Git precisou ser completado"
		case !imported:
			res.MetadataNote = "não migrados"
		default:
			res.MetadataNote = "não garantidos (importador não concluiu)"
		}
	}
	if res.ArchivedProject.ID != 0 && proj != nil && res.Status != StatusFailed {
		m.restoreGitLabOnlyWork(res.ArchivedProject, *proj, workdir, res)
	}

	took := terminal.Elapsed(t0)
	switch res.Status {
	case StatusComplete:
		ui.Print(stepIndent + ui.Green("✔") + " " + ui.Green(fmt.Sprintf("completo: Git verificado + metadados pelo importador (%s)", took)))
	case StatusPartial:
		ui.Print(stepIndent + ui.Yellow("◐") + " " + ui.Yellow(fmt.Sprintf("parcial: Git verificado; metadados %s (%s)", res.MetadataNote, took)))
	case StatusSkipped:
		ui.Print(stepIndent + ui.Dim("↷") + " já estava completo; nada enviado")
	default:
		ui.Print(stepIndent + ui.Red("✗") + " " + ui.Red(fmt.Sprintf("falhou (%s)", took)))
	}
	return res
}
