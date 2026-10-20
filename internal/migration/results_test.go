package migration

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ---- arquivo de resultados (results/)
type savedResults struct {
	Execucao     RunInfo     `json:"execucao"`
	Repositorios []ResultRow `json:"repositorios"`
}

// results: JSON e linhas do CSV gravados em results/ pela última execução.
func (s *testEnv) results() (savedResults, [][]string) {
	t := s.t
	paths, _ := filepath.Glob(filepath.Join(s.root, "results", "*.json"))
	if len(paths) != 1 {
		t.Fatalf("esperado 1 JSON em results/, achou %v", paths)
	}
	var data savedResults
	raw, _ := os.ReadFile(paths[0])
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(strings.TrimSuffix(paths[0], ".json") + ".csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return data, rows
}

func TestResultadoEmArquivoNoDryRun(t *testing.T) {
	s := newTestEnv(t)
	code, out := s.run(runCfg{args: []string{"--dry-run"}, hasGlab: true})
	if code != 0 || !strings.Contains(out, "Resultado por repositório") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	data, rows := s.results()
	if !data.Execucao.DryRun || data.Execucao.CodigoSaida != 0 || data.Execucao.GitLab != "tester" {
		t.Fatalf("execucao = %+v", data.Execucao)
	}
	if len(data.Repositorios) != 1 {
		t.Fatalf("repositorios = %+v", data.Repositorios)
	}
	row := data.Repositorios[0]
	if row.Origem != "ws/repo" || row.Destino != ns+"/repo" || row.Acao != ActionImport || row.Estado != StatusPending ||
		!strings.Contains(row.Motivo, "dry-run") {
		t.Fatalf("row = %+v", row)
	}
	if !reflect.DeepEqual(rows[0], resultFields) || rows[1][3] != string(StatusPending) {
		t.Fatalf("csv = %v", rows)
	}
}

func TestResultadoEmArquivoAposMigrar(t *testing.T) {
	s := newTestEnv(t)
	if code, out := s.run(runCfg{args: []string{"--yes"}, hasGlab: true}); code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	data, rows := s.results()
	row := data.Repositorios[0]
	if row.Estado != StatusComplete || row.URL != "https://gitlab.com/"+ns+"/repo" || row.API != "concluída" {
		t.Fatalf("row = %+v", row)
	}
	if rows[1][3] != string(StatusComplete) {
		t.Fatalf("csv = %v", rows)
	}
}

func TestResultadoEmArquivoComBloqueadoEIgnorado(t *testing.T) {
	s := newTestEnv(t)
	s.makeSource("ws/outro")
	s.gl.addProject(ns+"/outro", "none", "") // projeto vazio pré-existente: bloqueado
	makeBare(t, filepath.Join(s.bbRoot, "ws/vazio.git"))
	code, out := s.run(runCfg{args: []string{"--yes"}, hasGlab: true, list: "ws/repo\nws/outro\nws/vazio\n"})
	if code != 1 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	_, rows := s.results()
	states := map[string][2]string{}
	for _, r := range rows[1:] {
		states[r[0]] = [2]string{r[3], r[5]}
	}
	if states["ws/repo"][0] != string(StatusComplete) {
		t.Fatalf("ws/repo = %v", states["ws/repo"])
	}
	if states["ws/outro"][0] != string(StatusBlocked) || !strings.Contains(states["ws/outro"][1], string(ActionEmptyDest)) {
		t.Fatalf("ws/outro = %v", states["ws/outro"])
	}
	if states["ws/vazio"] != [2]string{string(StatusSkipped), "vazio no Bitbucket (sem nenhum branch)"} {
		t.Fatalf("ws/vazio = %v", states["ws/vazio"])
	}
}

func TestResultadoEmArquivoQuandoOImportadorParaTudo(t *testing.T) {
	s := newTestEnv(t)
	if code, out := s.run(runCfg{args: []string{"--yes"}, prStatus: 403, hasGlab: true}); code != 2 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	_, rows := s.results()
	if rows[1][3] != string(StatusPending) || !strings.Contains(rows[1][5], "importador indisponível") {
		t.Fatalf("csv = %v", rows)
	}
}
