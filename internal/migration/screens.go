package migration

import (
	"fmt"
	"os"
	"strings"

	"github.com/1roody/bb2glab-migrate/internal/git"
	"github.com/1roody/bb2glab-migrate/internal/terminal"
)

func (m *Migration) projectURL(repo *Repo) string {
	if repo.Project.WebURL != "" {
		return repo.Project.WebURL
	}
	return "https://" + m.gitlab.Host + "/" + repo.Dest
}

func printDivergence(ui *terminal.UI, divergent []git.Divergence, indent string) {
	const limit = 5
	for _, d := range divergent[:min(limit, len(divergent))] {
		ui.Print(indent + ui.Dim(fmt.Sprintf("%s: origem %s ≠ destino %s", git.RefLabel(d.Ref), git.ShortSHA(d.SourceSHA), git.ShortSHA(d.DestSHA))))
	}
	if len(divergent) > limit {
		ui.Print(indent + ui.Dim(fmt.Sprintf("… e mais %d (lista completa no log)", len(divergent)-limit)))
		var lines []string
		for _, d := range divergent[limit:] {
			lines = append(lines, fmt.Sprintf("%s%s: origem %s ≠ destino %s", indent, git.RefLabel(d.Ref), d.SourceSHA, d.DestSHA))
		}
		ui.Log(strings.Join(lines, "\n"))
	}
}

const stepIndent = "      " // recuo das etapas de cada repositório

func dateOf(s string) string {
	if s == "" {
		return "?"
	}
	return terminal.Truncate(s, 10)
}

func planDescription(repo *Repo) string {
	switch repo.Action {
	case ActionImport:
		return "projeto novo em " + repo.Namespace() + " · importador (PRs)"
	case ActionRetryImport:
		return "apaga o projeto vazio da importação que falhou e importa de novo"
	case ActionReimport:
		id := "?"
		if repo.Project.ID != 0 {
			id = fmt.Sprint(repo.Project.ID)
		}
		return fmt.Sprintf("arquiva o atual como %s-pre-reimport-%s e importa com PRs", repo.Name(), id)
	case ActionPush:
		return fmt.Sprintf("destino vazio · %d ref(s) por Git", len(repo.SourceRefs))
	case ActionResume:
		return fmt.Sprintf("faltam %d de %d ref(s) · Git sem sobrescrever", len(repo.MissingRefs), len(repo.SourceRefs))
	}
	return fmt.Sprintf("%d ref(s) divergente(s) serão SOBRESCRITAS", len(repo.DivergentRefs))
}

// commonWorkspace: workspace do Bitbucket comum a todas as entradas ("" se houver mais de um).
func commonWorkspace(repos []*Repo) string {
	ws := ""
	for i, repo := range repos {
		cur := strings.SplitN(repo.Source, "/", 2)[0]
		if i > 0 && cur != ws {
			return ""
		}
		ws = cur
	}
	return ws
}

// shortSource: origem sem o workspace quando ele é comum a todos (já mostrado no cabeçalho).
func shortSource(repo *Repo, workspace string) string {
	if workspace != "" {
		return strings.SplitN(repo.Source, "/", 2)[1]
	}
	return repo.Source
}

func withAction(repos []*Repo, actions ...Action) []*Repo {
	var out []*Repo
	for _, repo := range repos {
		for _, a := range actions {
			if repo.Action == a {
				out = append(out, repo)
				break
			}
		}
	}
	return out
}

func needingMigration(repos []*Repo) []*Repo {
	var out []*Repo
	for _, repo := range repos {
		if repo.Action.NeedsMigration() {
			out = append(out, repo)
		}
	}
	return out
}

// printColumns: nomes em colunas conforme a largura do terminal (de cima para baixo, como o ls).
func printColumns(ui *terminal.UI, names []string, indent string, color terminal.ColorFunc) {
	width := 0
	for _, n := range names {
		width = max(width, terminal.RuneLen(n))
	}
	width += 3
	ncols := max(1, (terminal.Width()-terminal.RuneLen(indent))/width)
	rows := (len(names) + ncols - 1) / ncols
	for r := range rows {
		var b strings.Builder
		for i := r; i < len(names); i += rows {
			fmt.Fprintf(&b, "%-*s", width, names[i])
		}
		ui.Print(indent + color(strings.TrimRight(b.String(), " ")))
	}
}

func (m *Migration) saveDoneURLs(path string, repos []*Repo) error {
	var b strings.Builder
	for _, repo := range repos {
		b.WriteString(m.projectURL(repo) + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// printHeader: cabeçalho da execução (contas, destino, lista e modos ativos).
func (m *Migration) printHeader(repos []*Repo, destBase, gitlabUser string) {
	ui, opts, host := m.ui, m.opts, m.gitlab.Host
	ui.Log(fmt.Sprintf("bb2gl: lista=%s dry-run=%t force=%t yes=%t allow-git-only=%t retry-failed-imports=%t reimport=%t",
		opts.ListPath, opts.DryRun, opts.Force, opts.Yes, opts.AllowGitOnly, opts.RetryFailedImports, opts.Reimport))
	ui.Section("bb2gl · Bitbucket Cloud → GitLab SaaS")
	ui.Print(fmt.Sprintf("  %-12s%s", "Bitbucket", m.bitbucket.User))
	via := "(glab)"
	if m.gitlab.Token != "" {
		via = "(GITLAB_TOKEN)"
	}
	ui.Print(fmt.Sprintf("  %-12s%s @ %s %s", "GitLab", gitlabUser, host, ui.Dim(via)))
	dest := ui.Dim("(somente destinos explícitos no repos.txt)")
	if destBase != "" {
		dest = host + "/" + destBase
	}
	ui.Print(fmt.Sprintf("  %-12s%s", "Destino", dest))
	ui.Print(fmt.Sprintf("  %-12s%s · %d repositório(s)", "Lista", opts.ListPath, len(repos)))
	if ws := commonWorkspace(repos); ws != "" {
		ui.Print(fmt.Sprintf("  %-12s%s %s", "Workspace", ws, ui.Dim("(omitido nos nomes abaixo)")))
	}
	var modes []string
	for _, mode := range []struct {
		on    bool
		color terminal.ColorFunc
		text  string
	}{
		{opts.DryRun, ui.Cyan, "dry-run (nada será enviado)"},
		{opts.Force, ui.Red, "--force (sobrescreve refs divergentes)"},
		{opts.AllowGitOnly, ui.Yellow, "--allow-git-only (aceita sem PRs)"},
		{opts.RetryFailedImports, ui.Yellow, "--retry-failed-imports"},
		{opts.Reimport, ui.Yellow, "--reimport (arquiva e reimporta)"},
		{opts.Yes, ui.Cyan, "--yes (sem pergunta)"},
	} {
		if mode.on {
			modes = append(modes, mode.color(mode.text))
		}
	}
	if len(modes) > 0 {
		ui.Print(fmt.Sprintf("  %-12s%s", "Modo", strings.Join(modes, " · ")))
	}
}

// printCheckResults mostra cada categoria da verificação, com a sua cor.
func (m *Migration) printCheckResults(repos []*Repo) {
	ui := m.ui
	workspace := commonWorkspace(repos)
	srcName := func(repo *Repo) string { return shortSource(repo, workspace) }

	group := func(color terminal.ColorFunc, symbol string, items []*Repo, label string, detail func(*Repo) string,
		name func(*Repo) string, extra func(*Repo)) {
		if len(items) == 0 {
			return
		}
		ui.Print(fmt.Sprintf("  %s %s %s", color(symbol), ui.Bold(color(fmt.Sprint(len(items)))), color(label)))
		width := 0
		for _, repo := range items {
			width = max(width, terminal.RuneLen(name(repo)))
		}
		for _, repo := range items {
			ui.Print(fmt.Sprintf("      %s %s  %s", color("·"), color(fmt.Sprintf("%-*s", width, name(repo))), ui.Dim(detail(repo))))
			if extra != nil {
				extra(repo)
			}
		}
	}
	reason := func(repo *Repo) string { return repo.Reason }

	done := withAction(repos, ActionDone)
	// A URL já traz o nome do repositório: mostra o link no lugar do caminho do Bitbucket.
	group(ui.Blue, "↷", done, "já completos no GitLab (todas as refs conferem): ignorados",
		func(repo *Repo) string { return "última atividade " + dateOf(repo.Project.LastActivityAt) }, m.projectURL, nil)
	if len(done) > 0 {
		if err := m.saveDoneURLs(m.opts.ExistingOut, done); err != nil {
			ui.Warn(fmt.Sprintf("não foi possível gravar %s: %v", m.opts.ExistingOut, err), "      ")
		} else {
			ui.Print(fmt.Sprintf("      %s %d URL(s) salvas em %s", ui.Blue("↳"), len(done), ui.Bold(m.opts.ExistingOut)))
		}
		noPRs := 0
		for _, repo := range done {
			if !repo.Project.ImportedWithPRs() {
				noPRs++
			}
		}
		if noPRs > 0 {
			ui.Print(fmt.Sprintf("      %s %d deles foram migrados só por Git (sem PRs): %s traz os PRs (arquiva o projeto atual)",
				ui.Blue("↳"), noPRs, ui.Bold("--reimport")))
		}
	}
	group(ui.Yellow, "↷", withAction(repos, ActionSourceEmpty), "vazios no Bitbucket (sem nenhum branch): nada a migrar, ignorados",
		func(*Repo) string { return "0 refs na origem" }, srcName, nil)
	group(ui.Red, "⊘", withAction(repos, ActionDivergent), "com refs divergentes no destino: bloqueados (só com --force)",
		func(repo *Repo) string {
			return fmt.Sprintf("%d divergente(s), %d ausente(s) · %s", len(repo.DivergentRefs), len(repo.MissingRefs), m.projectURL(repo))
		}, srcName, func(repo *Repo) { printDivergence(ui, repo.DivergentRefs, "          ") })
	group(ui.Magenta, "⊘", withAction(repos, ActionImportRunning), "com importação em andamento: bloqueados", reason, srcName, nil)
	group(ui.Red, "⊘", withAction(repos, ActionPreviousImportFailed), "com importação anterior que falhou: bloqueados", reason, srcName, nil)
	group(ui.Red, "⊘", withAction(repos, ActionEmptyDest), "com projeto vazio pré-existente: bloqueados (o importador só cria projetos novos)",
		reason, srcName, nil)
	group(ui.Red, "⊘", withAction(repos, ActionUnverified), "não verificados: bloqueados (nada será enviado)", reason, srcName, nil)

	// Para migrar: o detalhe comum a vários repositórios aparece uma vez só, e os nomes em colunas.
	todo := needingMigration(repos)
	if len(todo) == 0 {
		return
	}
	ui.Print(fmt.Sprintf("  %s %s %s", ui.Green("➜"), ui.Bold(ui.Green(fmt.Sprint(len(todo)))), ui.Green("para migrar")))
	groups := map[string][]*Repo{}
	var order []string // agrupa pelo texto do plano, na ordem em que aparecem
	for _, repo := range todo {
		d := planDescription(repo)
		if _, ok := groups[d]; !ok {
			order = append(order, d)
		}
		groups[d] = append(groups[d], repo)
	}
	var singles []*Repo
	for _, d := range order {
		items := groups[d]
		if len(items) == 1 {
			singles = append(singles, items[0])
			continue
		}
		ui.Print(fmt.Sprintf("      %s %s %s %s", ui.Green("↳"), ui.Bold(fmt.Sprint(len(items))), ui.Dim("·"), d))
		names := make([]string, len(items))
		for i, repo := range items {
			names[i] = srcName(repo)
		}
		printColumns(ui, names, "        ", ui.Green)
	}
	width := 0
	for _, repo := range singles {
		width = max(width, terminal.RuneLen(srcName(repo)))
	}
	for _, repo := range singles {
		ui.Print(fmt.Sprintf("      %s %s  %s", ui.Green("·"), ui.Green(fmt.Sprintf("%-*s", width, srcName(repo))), ui.Dim(planDescription(repo))))
	}
}

// printPlan resume o que será feito: os projetos novos agrupados por grupo de destino; as
// demais ações (que mexem em projetos existentes) uma a uma, para revisão antes de confirmar.
func (m *Migration) printPlan(todo []*Repo, problem string) {
	ui, gl, opts := m.ui, m.gitlab, m.opts
	if opts.DryRun {
		ui.Section("Plano (dry-run)")
	} else {
		ui.Section("Confirmação")
	}
	workspace := commonWorkspace(todo)
	imports := map[string]int{}
	var nsOrder []string
	var others []*Repo
	for _, repo := range todo {
		if repo.Action != ActionImport {
			others = append(others, repo)
			continue
		}
		if imports[repo.Namespace()] == 0 {
			nsOrder = append(nsOrder, repo.Namespace())
		}
		imports[repo.Namespace()]++
	}
	biggest := len(others)
	for _, n := range imports {
		biggest = max(biggest, n)
	}
	width := len(fmt.Sprint(biggest))
	for _, ns := range nsOrder {
		ui.Print(fmt.Sprintf("  %s %s  %s", ui.Bold(ui.Green(fmt.Sprintf("%*d ×", width, imports[ns]))), ui.Green(string(ActionImport)),
			ui.Dim(fmt.Sprintf("→ %s/%s/<repo> · projeto novo · importador (PRs)", gl.Host, ns))))
	}
	for _, repo := range others {
		color := ui.Green
		switch repo.Action {
		case ActionOverwrite:
			color = ui.Red
		case ActionRetryImport, ActionReimport:
			color = ui.Yellow
		}
		ui.Print(fmt.Sprintf("  %s %s %s  %s  %s", strings.Repeat(" ", width), color("·"), ui.Bold(shortSource(repo, workspace)),
			color("["+string(repo.Action)+"]"), ui.Dim("→ "+m.projectURL(repo)+" · "+planDescription(repo))))
	}
	ui.Print("")
	usesImporter, hasImport, hasReimport := false, false, false
	for _, repo := range todo {
		usesImporter = usesImporter || repo.Action.UsesImporter()
		hasImport = hasImport || repo.Action == ActionImport
		hasReimport = hasReimport || repo.Action == ActionReimport
	}
	if usesImporter {
		if problem != "" {
			msg := "importador: " + problem + "."
			if hasImport {
				msg += " Com --allow-git-only, projetos novos são criados vazios + push Git (sem PRs/comentários/LFS)."
			}
			ui.Warn(msg, "  ")
		} else {
			ui.OK("importador: credenciais do Bitbucket validadas (e-mail + API token com acesso a PRs)", "  ")
		}
	}
	if hasReimport {
		ui.Warn("reimport: configurações do projeto atual (membros, branches protegidas, variáveis de CI, "+
			"integrações) não são copiadas e o ID do projeto muda", "  ")
	}
	total := fmt.Sprintf("  Total: %s repositório(s) · push com ci.skip (não dispara pipelines)", ui.Bold(fmt.Sprint(len(todo))))
	if opts.Force {
		total += ui.Red(" · --force")
	}
	ui.Print(total)
}

func (m *Migration) askConfirmation(n int) bool {
	ui := m.ui
	if m.opts.Yes {
		ui.Print(fmt.Sprintf("\n  Confirmado por --yes (%d repositório(s)).", n))
		return true
	}
	if !m.deps.StdinTTY {
		ui.Error("confirmação necessária, mas stdin não é um terminal: revise a lista e use --yes.")
		return false
	}
	for {
		fmt.Fprintf(m.deps.Stdout, "\n  Migrar %d repositório(s)? Digite %s para confirmar ou %s para cancelar: ", n, ui.Bold("yes"), ui.Bold("no"))
		raw, ok := m.readLine()
		if !ok {
			return false
		}
		ans := strings.ToLower(strings.TrimSpace(raw))
		ui.Log(fmt.Sprintf("  resposta: '%s'", ans))
		switch ans {
		case "yes":
			return true
		case "no", "n":
			return false
		}
		fmt.Fprintf(m.deps.Stdout, "  %s Digite exatamente 'yes' ou 'no'.\n", ui.Yellow("Resposta inválida."))
	}
}

func (m *Migration) printNothingToMigrate(repos []*Repo, destBase string) {
	ui := m.ui
	done, empty, blocked := 0, 0, 0
	for _, repo := range repos {
		switch {
		case repo.Action == ActionDone:
			done++
		case repo.Action == ActionSourceEmpty:
			empty++
		case repo.Action.IsBlocked():
			blocked++
		}
	}
	ui.Print("")
	switch {
	case blocked > 0:
		ui.Print(ui.Red("✗") + " " + ui.Bold(ui.Red(fmt.Sprintf("Nada foi migrado: %d repositório(s) bloqueado(s) na verificação (veja acima).", blocked))))
	case done == len(repos):
		where := m.gitlab.Host
		if destBase != "" {
			where += "/" + destBase
		}
		ui.Print(ui.Green("✔") + " " + ui.Bold(fmt.Sprintf("Todos os %d repositório(s) da lista já estão completos no destino (%s).", len(repos), where)))
	default:
		var parts []string
		if done > 0 {
			parts = append(parts, ui.Blue(fmt.Sprintf("%d já completo(s) no destino", done)))
		}
		if empty > 0 {
			parts = append(parts, ui.Yellow(fmt.Sprintf("%d vazio(s) no Bitbucket", empty)))
		}
		ui.Print(ui.Green("✔") + " " + ui.Bold("Nada a migrar:") + " " + strings.Join(parts, ", ") + ".")
	}
	ui.Print("  " + ui.Dim("Nenhuma alteração foi feita."))
}

func (m *Migration) printSummary(results []*Result, repos []*Repo, total string, interrupted bool) {
	ui := m.ui
	byStatus := func(s Status) []*Result {
		var out []*Result
		for _, r := range results {
			if r.Status == s {
				out = append(out, r)
			}
		}
		return out
	}
	var blocked []*Repo
	ignored := len(byStatus(StatusSkipped))
	for _, repo := range repos {
		if repo.Action.IsBlocked() {
			blocked = append(blocked, repo)
		}
		if repo.Action == ActionDone || repo.Action == ActionSourceEmpty {
			ignored++
		}
	}
	title := "Resumo"
	if interrupted {
		title += " (interrompido)"
	}
	ui.Section(title)
	row := func(mark, label string, n int, note string) {
		line := fmt.Sprintf("  %s %-12s%4d", mark, label, n)
		if note != "" {
			line += "  " + ui.Dim(note)
		}
		ui.Print(line)
	}
	row(ui.Green("✓"), "Completos", len(byStatus(StatusComplete)), "Git verificado + metadados pelo importador")
	row(ui.Yellow("◐"), "Parciais", len(byStatus(StatusPartial)), "Git verificado; metadados não garantidos")
	row(ui.Red("✗"), "Falhas", len(byStatus(StatusFailed)), "")
	row(ui.Red("⊘"), "Bloqueados", len(blocked), "divergentes / não verificados / importação em andamento")
	row(ui.Dim("↷"), "Ignorados", ignored, "já completos ou vazios na origem")
	orDash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	for _, r := range append(byStatus(StatusPartial), byStatus(StatusFailed)...) {
		mark := ui.Red("✗")
		if r.Status == StatusPartial {
			mark = ui.Yellow("◐")
		}
		ui.Print("")
		ui.Print(fmt.Sprintf("  %s %s", mark, ui.Bold(r.Repo.Source)))
		ui.Print(fmt.Sprintf("      %s        %s", ui.Dim("API:"), orDash(r.ImportNote)))
		ui.Print(fmt.Sprintf("      %s        %s", ui.Dim("Git:"), orDash(r.GitNote)))
		if r.Status == StatusPartial {
			ui.Print(fmt.Sprintf("      %s  %s", ui.Dim("Metadados:"), r.MetadataNote))
		}
		for _, w := range r.Warnings {
			ui.Print(fmt.Sprintf("      %s %s", ui.Yellow("⚠"), w))
		}
	}
	if len(blocked) > 0 {
		ui.Print("")
	}
	for _, repo := range blocked {
		line := fmt.Sprintf("  %s %s: %s", ui.Red("⊘"), repo.Source, repo.Action)
		if repo.Reason != "" {
			line += " · " + repo.Reason
		}
		ui.Print(line)
	}
	var archived []*Result
	for _, r := range results {
		if r.ArchivedProject.ID != 0 {
			archived = append(archived, r)
		}
	}
	if len(archived) > 0 {
		ui.Print("")
		ui.Print(fmt.Sprintf("  %s Projetos anteriores arquivados (reaplicar as configurações no projeto novo):", ui.Yellow("↳")))
		for _, r := range archived {
			ui.Print(fmt.Sprintf("      %s %s", ui.Dim("·"), r.ArchivedProject.PathWithNamespace))
		}
	}
	ui.Print("")
	ui.Print("  Tempo total: " + total)
	if ui.Path() != "" {
		ui.Print("  Log completo (saída bruta dos comandos): " + ui.Bold(ui.Path()))
	}
}

// exitCode: 0 tudo completo (ou nada a migrar), 1 alguma falha ou bloqueio, 3 só parciais.
func exitCode(results []*Result, repos []*Repo) int {
	for _, repo := range repos {
		if repo.Action.IsBlocked() {
			return 1
		}
	}
	partial := false
	for _, r := range results {
		switch r.Status {
		case StatusFailed:
			return 1
		case StatusPartial:
			partial = true
		}
	}
	if partial {
		return 3
	}
	return 0
}
