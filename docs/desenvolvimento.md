# Desenvolvimento

[← README](../README.md)

## Build

```bash
make build     # ./bb2gl
make dist      # binários de Linux e macOS em dist/
make vet       # gofmt + go vet
make clean
```

Requer Go 1.24+. A única dependência externa é `golang.org/x/term` (detecção de terminal e largura da tela).

## Estrutura do código

```
cmd/bb2gl/main.go          entrada do comando (Ctrl+C) → internal/migration
internal/
├── migration/             o fluxo da migração
│   ├── options.go         opções de linha de comando e .env
│   ├── repolist.go        repos.txt e as ações/estados de cada repositório
│   ├── run.go             fluxo: pré-requisitos → verificação → confirmação → migração → resumo
│   ├── check.go           verificação (somente leitura)
│   ├── migrate.go         migração: importador do GitLab e complemento por Git
│   ├── reimport.go        --retry-failed-imports e --reimport
│   ├── screens.go         telas: cabeçalho, verificação, plano, confirmação, resumo
│   ├── results.go         results/ (JSON e CSV)
│   ├── search.go          --search / --missing / --add
│   └── *_test.go          testes (fakes_test.go: GitLab simulado e repositórios Git locais)
├── gitlab/                cliente da API do GitLab (GITLAB_TOKEN ou glab)
├── bitbucket/             cliente da API do Bitbucket
├── git/                   comandos git e comparação de refs
└── terminal/              tela colorida, spinner e log
```

O GitLab de destino é o gitlab.com (`DefaultHost`, em `internal/gitlab/client.go`).

## Testes

```bash
make test               # ou: go test ./...
go test -race ./...
```

Rodam sem chamadas reais: Bitbucket e GitLab viram repositórios Git locais (bare) e a API do GitLab é simulada (`internal/migration/fakes_test.go`); o modo `GITLAB_TOKEN` e a API do Bitbucket usam um servidor HTTP local (`httptest`). Só precisam do `git`. Cobrem:
- sucesso da API;
- falha antes e depois de criar o projeto;
- tentativa de importação duplicada;
- importação pendente/timeout e relações com falha;
- `--retry-failed-imports`, inclusive com exclusão adiada;
- `--reimport` preservando o trabalho feito no GitLab, e o rename desfeito quando a importação é recusada;
- parada antes de alterar quando o importador está indisponível, exigência de `--yes` sem terminal e cancelamento na confirmação;
- refs parciais, erro de `ls-remote` na origem, no destino e na verificação final;
- divergência com e sem `--force`;
- destino ausente, grupo ausente, erro do GitLab e reexecução;
- `GITLAB_TOKEN` sem `glab` (API direta, caminho codificado, Git por header, 404 vs. erro) e a falta de `glab` sem token;
- arquivo de resultados (JSON e CSV) no dry-run, depois de migrar, com bloqueados e ignorados, e quando o importador para tudo;
- `repos.txt`, `.env`, nomes de projeto, destinos duplicados, códigos de saída e opções antes/depois da lista.
