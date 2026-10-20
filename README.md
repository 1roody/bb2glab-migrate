# bb2gl: migração do Bitbucket Cloud para o GitLab

Migra repositórios do Bitbucket Cloud para o gitlab.com usando o **importador oficial do GitLab** (API). O importador cria o projeto e traz branches, tags, histórico, PRs com comentários, descrição e LFS. Se uma importação concluída deixar refs faltando, o `bb2gl` completa por Git (clone + push).

Antes de alterar qualquer coisa, o `bb2gl` verifica (somente leitura) a origem e o destino de cada repositório da lista e mostra o plano para confirmação. É um binário único, sem dependências além do `git`.

## Instalação

```bash
make build                 # gera ./bb2gl (precisa do Go 1.24+)
# ou, sem clonar:
go install github.com/1roody/bb2glab-migrate/cmd/bb2gl@latest   # instala o bb2gl em $(go env GOPATH)/bin
```

Para outro sistema: `GOOS=darwin GOARCH=arm64 go build -o bb2gl ./cmd/bb2gl` (ou `make dist`, que gera os binários de Linux e macOS em `dist/`).

## Quickstart

```bash
# 1. Credenciais: preencha BITBUCKET_USER (e-mail), BITBUCKET_TOKEN, GITLAB_TOKEN e GITLAB_DEST_PATH
cp .env.example .env

# 2. Repositórios a migrar: um workspace/repo por linha (o exemplo usa nomes fictícios)
cp repos.txt.example repos.txt

# 3. Simulação: verifica tudo e mostra o plano, sem alterar nada
./bb2gl --dry-run

# 4. Migração: mostra o plano de novo e pede 'yes'
./bb2gl
```

O `.env` e o `repos.txt` são lidos do diretório atual (veja `--env-file` e o argumento da lista para usar outros).

Antes do passo 4, quem tem PRs ou comentários no Bitbucket precisa ligar a conta ao GitLab. Sem isso, os PRs ficam no nome de quem rodou o `bb2gl` e isso não tem correção depois. Veja [Autoria dos PRs](docs/autoria-prs.md).

## Pré-requisitos

- `git` e o binário `bb2gl` (veja [Instalação](#instalação)).
- **Bitbucket:** API token (classic) com os escopos `read:repository:bitbucket` e `read:pullrequest:bitbucket`.
- **GitLab:** personal access token com escopo `api` em `GITLAB_TOKEN` **ou** o [`glab`](https://gitlab.com/gitlab-org/cli) autenticado. A conta precisa ser **Maintainer** no grupo de destino (**Owner** para `--retry-failed-imports`).

Passo a passo das credenciais, variáveis do `.env` e formato do `repos.txt`: [docs/configuracao.md](docs/configuracao.md).

## O que esperar

**Os PRs só vêm pelo importador, e ele só cria projetos novos.** Por isso, por padrão, o `bb2gl` nunca deixa um projeto sem PRs:
- se as credenciais do importador forem inválidas, ele para antes da confirmação;
- se uma importação falhar, o repositório termina como falha, sem criar projeto vazio;
- se já existir um projeto vazio no destino, ele bloqueia.

Para aceitar migrar sem PRs, use `--allow-git-only`.

Nada é alterado antes da confirmação, e é seguro rodar de novo: o que está completo é ignorado e o que está incompleto é retomado.

Ao final de cada execução (inclusive `--dry-run`), o resultado de cada repositório (origem, destino, ação, estado, URL e motivo) fica em `results/`, em JSON e CSV. Veja [Arquivo de resultados](docs/migracao.md#arquivo-de-resultados-results).

## Comandos comuns

| Para... | Rode |
|---|---|
| Ver o plano sem alterar nada | `bb2gl --dry-run` |
| Migrar (pede `yes`) | `bb2gl` |
| Migrar sem perguntar (automação) | `bb2gl --yes` |
| Usar outra lista | `bb2gl outra-lista.txt` |
| Refazer importações que falharam | `bb2gl --retry-failed-imports` |
| Trazer PRs para projetos migrados só por Git | `bb2gl --reimport` |
| Aceitar migrar sem PRs | `bb2gl --allow-git-only` |
| Procurar repositórios fora do `repos.txt` | `bb2gl --search termo` |
| Ver todas as opções | `bb2gl --help` |

As opções podem vir antes ou depois do arquivo da lista (`bb2gl repos.txt --dry-run` também funciona).

Códigos de saída: `0` completo · `1` falha ou bloqueio · `2` erro de uso ou pré-requisito · `3` só parciais · `130` cancelado.

## Documentação

| Documento | Conteúdo |
|---|---|
| [Configuração](docs/configuracao.md) | Pré-requisitos, `.env`, autenticação no GitLab (token ou `glab`) e `repos.txt`. |
| [Como a migração funciona](docs/migracao.md) | Verificação, confirmação, importador e fallback Git, `--reimport`, resultados, opções, log e o que é migrado. |
| [Autoria dos PRs](docs/autoria-prs.md) | O que cada pessoa precisa fazer antes da importação para os PRs ficarem no nome certo. |
| [Buscar repositórios fora da lista](docs/busca-repositorios.md) | `--search`, `--missing` e `--add`. |
| [Problemas frequentes](docs/problemas-frequentes.md) | Mensagens de erro e o que fazer. |
| [Desenvolvimento](docs/desenvolvimento.md) | Estrutura do código, build e testes. |
