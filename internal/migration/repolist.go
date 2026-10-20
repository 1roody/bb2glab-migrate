package migration

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/1roody/bb2glab-migrate/internal/git"
	"github.com/1roody/bb2glab-migrate/internal/gitlab"
)

// Action é a ação decidida na verificação para cada repositório (Repo.Action).
type Action string

const (
	ActionImport               Action = "importar via API"               // destino não existe
	ActionPush                 Action = "push Git"                       // destino existe e está vazio
	ActionResume               Action = "completar refs"                 // destino tem parte das refs, sem divergência
	ActionOverwrite            Action = "sobrescrever (--force)"         // destino com refs divergentes + --force
	ActionDone                 Action = "concluído"                      // todas as refs da origem já estão no destino
	ActionSourceEmpty          Action = "vazio no Bitbucket"             // origem sem nenhuma ref
	ActionDivergent            Action = "divergente"                     // bloqueado: refs com SHA diferente
	ActionImportRunning        Action = "importação em andamento"        // bloqueado: importação anterior ainda rodando
	ActionUnverified           Action = "não verificado"                 // bloqueado: falha ao consultar origem/destino
	ActionEmptyDest            Action = "projeto vazio pré-existente"    // bloqueado: o importador só cria projetos novos
	ActionPreviousImportFailed Action = "importação anterior falhou"     // bloqueado sem --retry-failed-imports/--reimport
	ActionRetryImport          Action = "reimportar (importação falhou)" // --retry-failed-imports: apaga o vazio e importa
	ActionReimport             Action = "reimportar com PRs"             // --reimport: arquiva o atual e importa
)

// NeedsMigration: ações que alteram o destino (entram na confirmação).
func (a Action) NeedsMigration() bool {
	switch a {
	case ActionImport, ActionRetryImport, ActionReimport, ActionPush, ActionResume, ActionOverwrite:
		return true
	}
	return false
}

// UsesImporter: ações que chamam o importador oficial do GitLab.
func (a Action) UsesImporter() bool {
	return a == ActionImport || a == ActionRetryImport || a == ActionReimport
}

// IsBlocked: ações que impedem a migração (contam como falha no código de saída).
func (a Action) IsBlocked() bool {
	switch a {
	case ActionDivergent, ActionImportRunning, ActionUnverified, ActionEmptyDest, ActionPreviousImportFailed:
		return true
	}
	return false
}

// Status é o resultado final de cada repositório migrado (Result.Status).
type Status string

const (
	StatusComplete Status = "completo"
	StatusPartial  Status = "parcial"
	StatusFailed   Status = "falha"
	StatusSkipped  Status = "ignorado"
)

// Repo é uma linha do repos.txt e o que a verificação descobriu sobre ela.
type Repo struct {
	Source        string // workspace/repo
	Dest          string // grupo[/subgrupo]/projeto
	Line          int
	Action        Action
	Reason        string         // detalhe do bloqueio
	Project       gitlab.Project // dados do projeto no GitLab (ID 0 = não existe)
	NamespaceID   int            // grupo de destino (projetos a criar)
	SourceRefs    git.Refs
	MissingRefs   []string         // refs da origem ausentes no destino
	DivergentRefs []git.Divergence // refs com SHA diferente
}

func newRepo(src, dst string, line int) *Repo {
	return &Repo{Source: src, Dest: dst, Line: line, Action: ActionUnverified, SourceRefs: git.Refs{}}
}

func (repo *Repo) Namespace() string { return repo.Dest[:strings.LastIndex(repo.Dest, "/")] }

func (repo *Repo) Name() string { return repo.Dest[strings.LastIndex(repo.Dest, "/")+1:] }

// Result é o resultado de um repositório processado na fase de migração.
type Result struct {
	Repo            *Repo
	Status          Status
	ImportNote      string // tentativa do importador
	GitNote         string // fallback Git
	MetadataNote    string // metadados (PRs, comentários, descrição, LFS)
	ImportOK        bool   // importador concluiu sem falhas
	PushedRefs      int    // refs enviadas por Git
	Warnings        []string
	ArchivedProject gitlab.Project // --reimport: projeto anterior, renomeado e arquivado (ID 0 = nenhum)
}

func (r *Result) addWarning(msg string) { r.Warnings = append(r.Warnings, msg) }

var (
	srcRE  = regexp.MustCompile(`^[^/\s]+/[^/\s]+$`)
	dstRE  = regexp.MustCompile(`^[^/\s]+(/[^/\s]+)+$`)
	baseRE = regexp.MustCompile(`^[^/\s]+(/[^/\s]+)*$`)
	hostRE = regexp.MustCompile(`^https?://([^/]*@)?(www\.)?(bitbucket\.org|gitlab\.com)/`)
	// Nome de projeto novo (o importador usa como path): começa e termina com letra/dígito,
	// sem caracteres especiais seguidos.
	newNameRE = regexp.MustCompile(`^[A-Za-z0-9]([._-]?[A-Za-z0-9])*$`)
)

// normalizeRepoPath: URL ou caminho -> grupo/.../projeto (sem host, sem .git e sem barras nas pontas).
func normalizeRepoPath(p string) string {
	p = hostRE.ReplaceAllString(strings.TrimSpace(p), "")
	return strings.Trim(strings.TrimSuffix(strings.Trim(p, "/"), ".git"), "/")
}

// repoListFields: campos de uma linha do repos.txt, sem comentário e sem o "->" opcional.
func repoListFields(raw string) []string {
	before, _, _ := strings.Cut(raw, "#")
	var parts []string
	for _, p := range strings.Fields(before) {
		if p != "->" && p != "→" {
			parts = append(parts, p)
		}
	}
	return parts
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

// parseRepoList lê o repos.txt: "<workspace>/<repo> [<grupo>/.../<projeto>]" por linha.
func parseRepoList(path, destBase string) ([]*Repo, []string, error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, nil, err
	}
	var repos []*Repo
	var errs []string
	for i, raw := range lines {
		n := i + 1
		parts := repoListFields(raw)
		if len(parts) == 0 {
			continue
		}
		if len(parts) > 2 {
			errs = append(errs, fmt.Sprintf("linha %d: esperado 'workspace/repo [grupo/projeto]' -> '%s'", n, strings.TrimSpace(raw)))
			continue
		}
		src := normalizeRepoPath(parts[0])
		var dst string
		switch {
		case len(parts) == 2:
			dst = normalizeRepoPath(parts[1])
		case destBase != "" && srcRE.MatchString(src):
			dst = destBase + "/" + strings.SplitN(src, "/", 2)[1]
		default:
			errs = append(errs, fmt.Sprintf("linha %d: sem destino e GITLAB_DEST_PATH não definido -> '%s'", n, strings.TrimSpace(raw)))
			continue
		}
		switch {
		case !srcRE.MatchString(src):
			errs = append(errs, fmt.Sprintf("linha %d: origem inválida '%s' (esperado workspace/repo)", n, src))
		case !dstRE.MatchString(dst):
			errs = append(errs, fmt.Sprintf("linha %d: destino inválido '%s' (esperado grupo/projeto, sem barra inicial)", n, dst))
		default:
			repos = append(repos, newRepo(src, dst, n))
		}
	}
	return repos, errs, nil
}

// listedSources: origens (workspace/repo, minúsculas) já presentes na lista, sem exigir destino.
func listedSources(path string) (map[string]bool, error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	found := map[string]bool{}
	for _, raw := range lines {
		if parts := repoListFields(raw); len(parts) > 0 {
			found[strings.ToLower(normalizeRepoPath(parts[0]))] = true
		}
	}
	return found, nil
}

// findDuplicateDests: destinos repetidos na lista (o GitLab não diferencia maiúsculas em paths).
func findDuplicateDests(repos []*Repo) []string {
	seen := map[string][]string{}
	var order []string
	for _, repo := range repos {
		key := strings.ToLower(repo.Dest)
		if _, ok := seen[key]; !ok {
			order = append(order, key)
		}
		seen[key] = append(seen[key], repo.Dest)
	}
	var out []string
	for _, key := range order {
		if len(seen[key]) < 2 {
			continue
		}
		uniq := map[string]bool{}
		var names []string
		for _, d := range seen[key] {
			if !uniq[d] {
				uniq[d] = true
				names = append(names, d)
			}
		}
		sort.Strings(names)
		out = append(out, strings.Join(names, " = "))
	}
	sort.Strings(out)
	return out
}

// invalidProjectName: motivo pelo qual name não serve como path de projeto novo ("" se serve).
func invalidProjectName(name string) string {
	if !newNameRE.MatchString(name) {
		return fmt.Sprintf("nome '%s' inválido para projeto novo (só letras, dígitos, '.', '_' e '-'; "+
			"começa/termina com letra ou dígito; sem caracteres especiais seguidos)", name)
	}
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".git") || strings.HasSuffix(lower, ".atom") {
		return fmt.Sprintf("nome '%s' inválido para projeto novo (não pode terminar em .git/.atom)", name)
	}
	return ""
}
