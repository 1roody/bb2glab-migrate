package migration

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Estado no arquivo de resultados de quem não chegou a ser migrado nesta execução.
const (
	StatusBlocked Status = "bloqueado"
	StatusPending Status = "pendente"
)

var resultFields = []string{"origem", "destino", "acao", "estado", "url", "motivo",
	"api", "git", "metadados", "avisos", "projeto_anterior"}

// ResultRow: uma linha por repositório da lista, para cada time conferir a sua migração.
type ResultRow struct {
	Origem          string   `json:"origem"`
	Destino         string   `json:"destino"`
	Acao            Action   `json:"acao"`
	Estado          Status   `json:"estado"`
	URL             string   `json:"url"`
	Motivo          string   `json:"motivo"`
	API             string   `json:"api"`
	Git             string   `json:"git"`
	Metadados       string   `json:"metadados"`
	Avisos          []string `json:"avisos"`
	ProjetoAnterior string   `json:"projeto_anterior"`
}

func (r ResultRow) csvRecord() []string {
	return []string{r.Origem, r.Destino, string(r.Acao), string(r.Estado), r.URL, r.Motivo,
		r.API, r.Git, r.Metadados, strings.Join(r.Avisos, " | "), r.ProjetoAnterior}
}

// RunInfo: bloco "execucao" do JSON.
type RunInfo struct {
	Data        string `json:"data"`
	Lista       string `json:"lista"`
	DryRun      bool   `json:"dry_run"`
	GitLab      string `json:"gitlab"`
	CodigoSaida int    `json:"codigo_saida"`
}

// buildResultRows monta as linhas do arquivo de resultados. pending: motivo dos que estavam para
// migrar e não foram (dry-run, cancelado, interrompido, importador indisponível).
func buildResultRows(repos []*Repo, results []*Result, pending string) []ResultRow {
	byRepo := map[*Repo]*Result{}
	for _, r := range results {
		byRepo[r.Repo] = r
	}
	rows := make([]ResultRow, 0, len(repos))
	for _, repo := range repos {
		row := ResultRow{Origem: repo.Source, Destino: repo.Dest, Acao: repo.Action, URL: repo.Project.WebURL, Avisos: []string{}}
		r := byRepo[repo]
		switch {
		case r != nil:
			row.Estado = r.Status
			switch r.Status {
			case StatusComplete:
			case StatusPartial:
				row.Motivo = "metadados " + r.MetadataNote
			case StatusSkipped:
				row.Motivo = "já estava completo; nada enviado"
			default:
				row.Motivo = firstNonEmpty(r.GitNote, r.ImportNote, "detalhes no log")
			}
			row.API, row.Git, row.Metadados = r.ImportNote, r.GitNote, r.MetadataNote
			row.Avisos = append(row.Avisos, r.Warnings...)
			row.ProjetoAnterior = r.ArchivedProject.PathWithNamespace
		case repo.Action.IsBlocked():
			row.Estado, row.Motivo = StatusBlocked, string(repo.Action)
			if repo.Reason != "" {
				row.Motivo += ": " + repo.Reason
			}
		case repo.Action == ActionDone:
			row.Estado, row.Motivo = StatusSkipped, "já completo no GitLab (todas as refs conferem)"
			if !repo.Project.ImportedWithPRs() {
				row.Motivo += "; migrado só por Git (sem PRs): --reimport traz os PRs"
			}
		case repo.Action == ActionSourceEmpty:
			row.Estado, row.Motivo = StatusSkipped, "vazio no Bitbucket (sem nenhum branch)"
		default:
			row.Estado, row.Motivo = StatusPending, pending
		}
		rows = append(rows, row)
	}
	return rows
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// writeResultFiles grava <dir>/bb2gl-<stamp>.json e .csv com as mesmas linhas.
func writeResultFiles(dir, stamp string, rows []ResultRow, info RunInfo) (string, string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	base := filepath.Join(dir, "bb2gl-"+stamp)
	jsonPath, csvPath := base+".json", base+".csv"

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(struct {
		Execucao     RunInfo     `json:"execucao"`
		Repositorios []ResultRow `json:"repositorios"`
	}{info, rows}); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(jsonPath, buf.Bytes(), 0o644); err != nil {
		return "", "", err
	}

	buf.Reset()
	w := csv.NewWriter(&buf)
	w.UseCRLF = true // fim de linha do RFC 4180: abre direto em planilhas
	w.Write(resultFields)
	for _, row := range rows {
		w.Write(row.csvRecord())
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(csvPath, buf.Bytes(), 0o644); err != nil {
		return "", "", err
	}
	return jsonPath, csvPath, nil
}
