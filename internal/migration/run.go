// Package migration é o fluxo do bb2gl:
//
//   - options.go   opções de linha de comando e .env
//   - repolist.go  repos.txt e o que se decide para cada repositório (ações, estados)
//   - run.go       fluxo principal: pré-requisitos → verificação → confirmação → migração → resumo
//   - check.go     fase de verificação (somente leitura)
//   - migrate.go   fase de migração: importador do GitLab e complemento por Git
//   - reimport.go  --retry-failed-imports e --reimport
//   - screens.go   telas: cabeçalho, verificação, plano, confirmação e resumo
//   - results.go   arquivo de resultados em results/ (JSON e CSV)
//   - search.go    --search / --missing / --add
package migration

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/1roody/bb2glab-migrate/internal/bitbucket"
	"github.com/1roody/bb2glab-migrate/internal/git"
	"github.com/1roody/bb2glab-migrate/internal/gitlab"
	"github.com/1roody/bb2glab-migrate/internal/terminal"
)

// Dependencies: o que o programa usa de fora (terminal, PATH, endereços das APIs). Os testes
// trocam por versões locais.
type Dependencies struct {
	Stdin               io.Reader
	Stdout, Stderr      io.Writer
	StdinTTY, StdoutTTY bool
	LookPath            func(string) (string, error)
	NewGitLabClient     func(host, token string) gitlab.API // nil: API direta com GITLAB_TOKEN, senão glab
	GitLabHost          string
	BitbucketAPI        string
	BitbucketGit        string
	PollInterval        time.Duration // intervalo entre consultas do estado de uma importação
	OnMigrate           func()        // chamado ao entrar na fase de migração
}

func DefaultDependencies() Dependencies {
	return Dependencies{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		StdinTTY:     term.IsTerminal(int(os.Stdin.Fd())),
		StdoutTTY:    term.IsTerminal(int(os.Stdout.Fd())),
		LookPath:     exec.LookPath,
		GitLabHost:   gitlab.DefaultHost,
		BitbucketAPI: bitbucket.DefaultAPI,
		BitbucketGit: bitbucket.DefaultGitBase,
		PollInterval: 5 * time.Second,
	}
}

// Migration: estado de uma execução, usado da verificação ao resumo.
type Migration struct {
	ctx            context.Context
	opts           *Options
	deps           Dependencies
	ui             *terminal.UI
	stdin          *bufio.Reader
	bitbucket      *bitbucket.Client
	gitlab         *gitlab.Client
	listRemoteRefs func(url string, env []string) (git.Refs, error) // substituível nos testes
}

// RunCLI interpreta os argumentos, executa e devolve o código de saída.
func RunCLI(ctx context.Context, args []string, deps Dependencies) int {
	opts, err := parseOptions(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(deps.Stdout, usageText)
		return 0
	}
	if err != nil {
		fmt.Fprintf(deps.Stderr, "Uso: bb2gl [opções] [repos.txt]\nerro: %v (veja bb2gl --help)\n", err)
		return 2
	}
	m := &Migration{ctx: ctx, opts: opts, deps: deps, ui: terminal.NewUI(deps.Stdout, deps.Stderr, deps.StdoutTTY),
		stdin: bufio.NewReader(deps.Stdin), listRemoteRefs: git.ListRemoteRefs}
	defer m.ui.Close()
	return m.run()
}

// run: pré-requisitos → verificação → confirmação → migração → resumo.
func (m *Migration) run() int {
	ui, opts := m.ui, m.opts
	start := time.Now()

	// Pré-requisitos (nada é tocado até aqui)
	if isFile(opts.EnvFile) {
		if err := loadEnvFile(opts.EnvFile); err != nil {
			ui.Error(fmt.Sprintf("não foi possível ler %s: %v", opts.EnvFile, err))
			return 2
		}
	}
	searching := len(opts.Search) > 0 || opts.Missing
	gitlabToken := strings.TrimSpace(os.Getenv("GITLAB_TOKEN"))
	// Com GITLAB_TOKEN, API e Git falam direto com o GitLab: o glab não é necessário.
	tools := []string{"git"}
	if !searching && gitlabToken == "" {
		tools = append(tools, "glab")
	}
	for _, tool := range tools {
		if _, err := m.deps.LookPath(tool); err != nil {
			msg := "comando obrigatório não encontrado: " + tool
			if tool == "glab" {
				msg += " (ou defina GITLAB_TOKEN para usar o GitLab sem o glab)"
			}
			ui.Error(msg)
			return 2
		}
	}
	bbUser, bbToken := os.Getenv("BITBUCKET_USER"), os.Getenv("BITBUCKET_TOKEN")
	if bbUser == "" || bbToken == "" {
		ui.Error(fmt.Sprintf("defina BITBUCKET_USER e BITBUCKET_TOKEN em %s (modelo: .env.example) ou no ambiente.", opts.EnvFile))
		return 2
	}
	m.bitbucket = bitbucket.New(bbUser, bbToken, os.Getenv("BITBUCKET_GIT_USER"))
	m.bitbucket.APIBase, m.bitbucket.GitBase = m.deps.BitbucketAPI, m.deps.BitbucketGit
	if searching {
		return m.searchBitbucket()
	}

	repos, destBase, code := m.loadRepoList()
	if code != 0 {
		return code
	}
	gitlabUser, code := m.connectGitLab(gitlabToken)
	if code != 0 {
		return code
	}
	// 401 = e-mail/token recusados para qualquer repositório: para antes de verificar a lista inteira.
	if m.bitbucket.APIStatus(repos[0].Source) == 401 {
		ui.Error("o Bitbucket recusou BITBUCKET_USER/BITBUCKET_TOKEN (HTTP 401): token inválido, expirado, " +
			"revogado ou de outra conta. Use um API token criado pela conta do e-mail em BITBUCKET_USER, " +
			"com os escopos read:repository:bitbucket e read:pullrequest:bitbucket.")
		return 2
	}
	m.printHeader(repos, destBase, gitlabUser)

	// Verificação (somente leitura): decide a ação de cada repositório comparando as refs.
	ui.Section("Verificação")
	m.checkAll(repos)
	importerProblem := ""
	for _, repo := range repos {
		if repo.Action.UsesImporter() {
			importerProblem = m.bitbucket.CheckImporterCredentials(repo.Source)
			break
		}
	}
	m.printCheckResults(repos)

	var results []*Result
	// finish grava o resultado por repositório (toda execução que passou da verificação) e devolve code.
	finish := func(code int, pendingReason string) int {
		info := RunInfo{Data: time.Now().Format("2006-01-02T15:04:05"), Lista: opts.ListPath,
			DryRun: opts.DryRun, GitLab: gitlabUser, CodigoSaida: code}
		jsonPath, csvPath, err := writeResultFiles(opts.ResultsDir, opts.Stamp, buildResultRows(repos, results, pendingReason), info)
		if err != nil {
			ui.Warn(fmt.Sprintf("não foi possível gravar o resultado em %s: %v", opts.ResultsDir, err), "  ")
		} else {
			ui.Print(fmt.Sprintf("  Resultado por repositório: %s e %s", ui.Bold(jsonPath), ui.Bold(filepath.Base(csvPath))))
		}
		return code
	}

	todo := needingMigration(repos)
	if len(todo) == 0 {
		m.printNothingToMigrate(repos, destBase)
		return finish(exitCode(nil, repos), "")
	}

	// Confirmação
	m.printPlan(todo, importerProblem)
	// Sem o importador não há PRs: só segue com --allow-git-only, e nunca para reimportações.
	reimports := len(withAction(todo, ActionRetryImport, ActionReimport)) > 0
	if importerProblem != "" && (reimports || !opts.AllowGitOnly) {
		ui.Print("")
		ui.Error("importador indisponível: " + importerProblem + ".")
		hint := " ou use --allow-git-only para migrar sem PRs."
		if reimports {
			hint = ". --reimport e --retry-failed-imports exigem o importador."
		}
		ui.PrintErr("  Sem ele os PRs não são migrados. Use um API token com read:repository:bitbucket e read:pullrequest:bitbucket" + hint)
		ui.PrintErr("  Nada foi alterado.")
		return finish(2, "não migrado: importador indisponível ("+importerProblem+")")
	}
	if opts.DryRun {
		if err := ui.OpenLog(opts.LogPath); err != nil {
			ui.Warn(fmt.Sprintf("não foi possível criar o log %s: %v", opts.LogPath, err), "  ")
		}
		ui.Print(fmt.Sprintf("\n  %s nada foi criado nem enviado. Log: %s", ui.Cyan("dry-run:"), ui.Bold(ui.Path())))
		return finish(exitCode(nil, repos), "dry-run: nada foi criado nem enviado")
	}
	if !m.askConfirmation(len(todo)) {
		ui.Print(fmt.Sprintf("\n  %s Nada foi alterado.", ui.Yellow("Cancelado.")))
		return finish(130, "cancelado na confirmação")
	}

	// Migração
	if err := ui.OpenLog(opts.LogPath); err != nil {
		ui.Error(fmt.Sprintf("não foi possível criar o log %s: %v", opts.LogPath, err))
		return 2
	}
	workdir, cleanup, err := m.prepareWorkdir()
	if err != nil {
		ui.Error(err.Error())
		return 2
	}
	defer cleanup()
	if m.deps.OnMigrate != nil {
		m.deps.OnMigrate()
	}
	ui.Section("Migração")
	for i, repo := range todo {
		if m.ctx.Err() != nil {
			break
		}
		m.printRepoHeader(i+1, len(todo), repo)
		results = append(results, m.migrateRepo(repo, workdir, importerProblem))
	}
	interrupted := m.ctx.Err() != nil
	if interrupted {
		ui.Print("")
		ui.Warn("Interrompido pelo usuário.", "  ")
	}

	m.printSummary(results, repos, terminal.Elapsed(start), interrupted)
	if interrupted {
		return finish(130, "interrompido antes de concluir; rode de novo")
	}
	return finish(exitCode(results, repos), "interrompido antes de concluir; rode de novo")
}

// loadRepoList lê e valida o repos.txt. code != 0: erro já mostrado.
func (m *Migration) loadRepoList() (repos []*Repo, destBase string, code int) {
	ui, opts := m.ui, m.opts
	if !isFile(opts.ListPath) {
		ui.Error("lista não encontrada: " + opts.ListPath)
		return nil, "", 2
	}
	destBase = m.resolveDestBase()
	if destBase != "" && !baseRE.MatchString(destBase) {
		ui.Error(fmt.Sprintf("GITLAB_DEST_PATH inválido: '%s' (formato: grupo/subgrupo)", destBase))
		return nil, "", 2
	}
	repos, problems, err := parseRepoList(opts.ListPath, destBase)
	if err != nil {
		ui.Error(fmt.Sprintf("não foi possível ler %s: %v", opts.ListPath, err))
		return nil, "", 2
	}
	if len(problems) > 0 {
		ui.Error(fmt.Sprintf("%d problema(s) em %s:", len(problems), opts.ListPath))
		for _, p := range problems {
			ui.PrintErr("  " + p)
		}
		return nil, "", 2
	}
	if len(repos) == 0 {
		ui.Error("nenhuma entrada em " + opts.ListPath)
		return nil, "", 2
	}
	if dups := findDuplicateDests(repos); len(dups) > 0 {
		ui.Error("destinos duplicados na lista (o GitLab não diferencia maiúsculas): " + strings.Join(dups, ", "))
		return nil, "", 2
	}
	return repos, destBase, 0
}

// resolveDestBase: GITLAB_DEST_PATH do .env/ambiente; se ausente, pergunta (terminal interativo).
func (m *Migration) resolveDestBase() string {
	base := normalizeRepoPath(os.Getenv("GITLAB_DEST_PATH"))
	if base != "" || !m.deps.StdinTTY {
		return base
	}
	fmt.Fprintln(m.deps.Stdout, "GITLAB_DEST_PATH não definido (grupo/subgrupo base no GitLab).")
	fmt.Fprint(m.deps.Stdout, "Path de destino no GitLab (ex.: meu-grupo/meu-subgrupo; vazio = só destinos explícitos no repos.txt): ")
	answer, _ := m.readLine()
	return normalizeRepoPath(answer)
}

// connectGitLab escolhe o cliente da API (GITLAB_TOKEN ou glab) e confere o login.
// Devolve o usuário do GitLab; code != 0: erro já mostrado.
func (m *Migration) connectGitLab(token string) (username string, code int) {
	host := m.deps.GitLabHost
	var client gitlab.API
	switch {
	case m.deps.NewGitLabClient != nil:
		client = m.deps.NewGitLabClient(host, token)
	case token != "":
		client = gitlab.NewTokenAPI(host, token)
	default:
		client = &gitlab.GlabAPI{Host: host}
	}
	m.gitlab = &gitlab.Client{API: client, Host: host, Token: token}
	var me struct {
		Username string `json:"username"`
	}
	if err := m.gitlab.Request("GET", "user", nil, &me); err != nil {
		if token != "" {
			m.ui.Error(fmt.Sprintf("o GitLab recusou GITLAB_TOKEN em %s (token inválido, expirado ou sem escopo api)\n  %s", host, gitlab.ErrorText(err)))
		} else {
			m.ui.Error(fmt.Sprintf("glab não está autenticado em %s (rode: glab auth login, ou defina GITLAB_TOKEN)\n  %s", host, gitlab.ErrorText(err)))
		}
		return "", 2
	}
	if me.Username == "" {
		return "?", 0
	}
	return me.Username, 0
}

// prepareWorkdir: --workdir, ou um diretório temporário apagado no fim.
func (m *Migration) prepareWorkdir() (dir string, cleanup func(), err error) {
	if dir = m.opts.Workdir; dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", nil, fmt.Errorf("não foi possível criar %s: %v", dir, err)
		}
		return dir, func() {}, nil
	}
	dir, err = os.MkdirTemp("", "bb2gl.")
	if err != nil {
		return "", nil, fmt.Errorf("não foi possível criar o diretório temporário: %v", err)
	}
	return dir, func() { os.RemoveAll(dir) }, nil
}

func (m *Migration) readLine() (string, bool) {
	line, err := m.stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimRight(line, "\r\n"), true
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
