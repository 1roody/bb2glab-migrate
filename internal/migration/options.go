package migration

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const usageText = `Uso: bb2gl [opções] [repos.txt]
Migra repositórios do Bitbucket Cloud para o GitLab: importador oficial (API) primeiro,
fallback Git (clone + push) para completar.

Opções:
  -n, --dry-run               só verifica e mostra o plano; não cria nem envia nada
  -y, --yes                   confirma a lista sem perguntar (automação); não implica --force
  -f, --force                 sobrescreve refs divergentes no destino (push com '+' só nessas refs)
      --allow-git-only        aceita migrar sem PRs: projeto vazio + push Git quando o importador
                              falhar ou não puder ser usado, e push em projeto vazio pré-existente
      --retry-failed-imports  apaga o projeto vazio deixado por uma importação que falhou e
                              importa de novo (requer Owner)
      --reimport              projetos migrados sem o importador (sem PRs): arquiva o atual como
                              <nome>-pre-reimport-<id>, importa com PRs no caminho original e
                              reenvia o que só existia no GitLab
      --import-timeout SEG    tempo máximo de espera por importação via API (padrão: 1800)
      --env-file ARQ          arquivo .env com as credenciais (padrão: .env no diretório atual)
      --workdir DIR           diretório para clones temporários (padrão: diretório temporário)
      --log ARQ               arquivo de log (padrão: logs/bb2gl-AAAAMMDD-HHMMSS.log)
      --results-dir DIR       onde gravar o resultado por repositório (JSON e CSV) de cada
                              execução (padrão: results)
      --existing-out ARQ      URLs no GitLab dos repositórios já completos
                              (padrão: repos-ja-no-gitlab.txt)

Busca no Bitbucket (somente leitura; não migra, não precisa do glab nem do GITLAB_TOKEN):
      --search TERMO          busca por nome no workspace inteiro (repetível) e mostra o que
                              está fora do repos.txt
      --missing               lista repositórios do(s) workspace(s) que não estão no repos.txt
      --workspace WS          workspace a varrer (padrão: os do repos.txt; repetível)
      --add                   acrescenta ao repos.txt os que faltam (pede yes)

Códigos de saída: 0 completo, 1 falha/bloqueio, 2 uso, 3 parcial (Git ok, metadados não
garantidos), 130 cancelado.
`

// Options: opções de linha de comando.
type Options struct {
	ListPath           string
	DryRun             bool
	Yes                bool
	Force              bool
	AllowGitOnly       bool
	RetryFailedImports bool
	Reimport           bool
	ImportTimeout      int // segundos
	Search             []string
	Missing            bool
	Workspaces         []string
	Add                bool
	EnvFile            string
	Workdir            string
	LogPath            string
	ResultsDir         string
	ExistingOut        string
	Stamp              string // AAAAMMDD-HHMMSS da execução (nome do log e dos resultados)
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// parseOptions aceita opções antes e depois do arquivo da lista.
func parseOptions(args []string) (*Options, error) {
	o := &Options{ImportTimeout: 1800, Stamp: time.Now().Format("20060102-150405")}
	fs := flag.NewFlagSet("bb2gl", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	for _, name := range []string{"n", "dry-run"} {
		fs.BoolVar(&o.DryRun, name, false, "")
	}
	for _, name := range []string{"y", "yes"} {
		fs.BoolVar(&o.Yes, name, false, "")
	}
	for _, name := range []string{"f", "force"} {
		fs.BoolVar(&o.Force, name, false, "")
	}
	fs.BoolVar(&o.AllowGitOnly, "allow-git-only", false, "")
	fs.BoolVar(&o.RetryFailedImports, "retry-failed-imports", false, "")
	fs.BoolVar(&o.Reimport, "reimport", false, "")
	fs.IntVar(&o.ImportTimeout, "import-timeout", 1800, "")
	fs.Var((*stringList)(&o.Search), "search", "")
	fs.BoolVar(&o.Missing, "missing", false, "")
	fs.Var((*stringList)(&o.Workspaces), "workspace", "")
	fs.BoolVar(&o.Add, "add", false, "")
	fs.StringVar(&o.EnvFile, "env-file", ".env", "")
	fs.StringVar(&o.Workdir, "workdir", "", "")
	fs.StringVar(&o.LogPath, "log", "logs/bb2gl-"+o.Stamp+".log", "")
	fs.StringVar(&o.ResultsDir, "results-dir", "results", "")
	fs.StringVar(&o.ExistingOut, "existing-out", "repos-ja-no-gitlab.txt", "")

	var positional, afterDashes []string
	for i, arg := range args {
		if arg == "--" {
			args, afterDashes = args[:i], args[i+1:]
			break
		}
	}
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
	positional = append(positional, afterDashes...)
	switch len(positional) {
	case 0:
		o.ListPath = "repos.txt"
	case 1:
		o.ListPath = positional[0]
	default:
		return nil, fmt.Errorf("argumentos não reconhecidos: %s", strings.Join(positional[1:], " "))
	}
	if o.ImportTimeout < 0 {
		return nil, errors.New("--import-timeout precisa ser >= 0")
	}
	return o, nil
}

// loadEnvFile carrega KEY=VALUE de um .env no ambiente, sem sobrescrever variáveis já definidas.
func loadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == value[len(value)-1] && (value[0] == '"' || value[0] == '\'') {
			value = value[1 : len(value)-1]
		}
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, value)
		}
	}
	return nil
}
