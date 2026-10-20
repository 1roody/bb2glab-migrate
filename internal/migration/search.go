package migration

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/1roody/bb2glab-migrate/internal/bitbucket"
)

// searchBitbucket (--search / --missing) consulta o Bitbucket e mostra o que está fora do repos.txt.
func (m *Migration) searchBitbucket() int {
	bb := m.bitbucket
	opts := m.opts
	listed := map[string]bool{}
	if info, err := os.Stat(opts.ListPath); err == nil && !info.IsDir() {
		var err error
		if listed, err = listedSources(opts.ListPath); err != nil {
			fmt.Fprintf(m.deps.Stderr, "erro: %v\n", err)
			return 2
		}
	}
	// A API do Bitbucket Cloud não lista workspaces/repos entre workspaces (endpoints globais
	// retornam 404): a busca varre o workspace inteiro, não só o que está no repos.txt.
	workspaces := opts.Workspaces
	if len(workspaces) == 0 {
		seen := map[string]bool{}
		for src := range listed {
			ws := strings.SplitN(src, "/", 2)[0]
			if !seen[ws] {
				seen[ws] = true
				workspaces = append(workspaces, ws)
			}
		}
		sort.Strings(workspaces)
	}
	if len(workspaces) == 0 {
		fmt.Fprintln(m.deps.Stderr, "erro: nenhum workspace conhecido no repos.txt; use --workspace.")
		return 2
	}

	repos := map[string]bitbucket.Repo{}
	for _, ws := range workspaces {
		list, err := bb.ListRepos(ws)
		if err != nil {
			fmt.Fprintf(m.deps.Stderr, "erro: %v (credenciais/escopo 'read:repository:bitbucket'? workspace correto?)\n", err)
			return 2
		}
		for _, r := range list {
			repos[strings.ToLower(r.FullName)] = r
		}
	}

	var terms []string
	for _, t := range opts.Search {
		terms = append(terms, strings.ToLower(t))
	}
	matches := func(r bitbucket.Repo, t string) bool {
		return strings.Contains(strings.ToLower(r.FullName), t) || strings.Contains(strings.ToLower(r.Name), t)
	}
	var hits []bitbucket.Repo
	for _, r := range repos {
		if len(terms) == 0 {
			hits = append(hits, r)
			continue
		}
		for _, t := range terms {
			if matches(r, t) {
				hits = append(hits, r)
				break
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool { return strings.ToLower(hits[i].FullName) < strings.ToLower(hits[j].FullName) })
	fmt.Fprintf(m.deps.Stdout, "Busca no Bitbucket (%s): %d repositório(s) visíveis, %d correspondem.\n",
		strings.Join(workspaces, ", "), len(repos), len(hits))

	var missing []bitbucket.Repo
	for _, r := range hits {
		if !listed[strings.ToLower(r.FullName)] {
			missing = append(missing, r)
		}
	}
	show := missing
	if len(terms) > 0 {
		show = hits
	}
	for _, r := range show {
		mark := "FORA lista "
		if listed[strings.ToLower(r.FullName)] {
			mark = "na lista   "
		}
		fmt.Fprintf(m.deps.Stdout, "  [%s] %s   (atualizado %s)\n", mark, r.FullName, dateOf(r.UpdatedOn))
	}
	// termos sem nenhum resultado
	for _, t := range terms {
		found := false
		for _, r := range repos {
			if matches(r, t) {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(m.deps.Stdout, "  [sem resultado] '%s' não encontrado em nenhum workspace acessível\n", t)
		}
	}
	fmt.Fprintf(m.deps.Stdout, "\n%d repositório(s) fora do %s.\n", len(missing), opts.ListPath)

	if opts.Add && len(missing) > 0 {
		if !m.deps.StdinTTY {
			fmt.Fprintln(m.deps.Stderr, "erro: --add exige terminal interativo.")
			return 2
		}
		fmt.Fprintf(m.deps.Stdout, "Acrescentar %d repositório(s) ao %s? Digite 'yes' para confirmar: ", len(missing), opts.ListPath)
		ans, ok := m.readLine()
		if !ok || strings.ToLower(strings.TrimSpace(ans)) != "yes" {
			fmt.Fprintln(m.deps.Stdout, "Nada foi alterado.")
			return 130
		}
		text, _ := os.ReadFile(opts.ListPath)
		f, err := os.OpenFile(opts.ListPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(m.deps.Stderr, "erro: %v\n", err)
			return 2
		}
		defer f.Close()
		if len(text) > 0 && text[len(text)-1] != '\n' {
			fmt.Fprintln(f)
		}
		for _, r := range missing {
			fmt.Fprintln(f, r.FullName)
		}
		fmt.Fprintf(m.deps.Stdout, "%d linha(s) acrescentada(s) em %s.\n", len(missing), opts.ListPath)
	}
	return 0
}
