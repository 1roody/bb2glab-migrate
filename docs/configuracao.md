# Configuração

[← README](../README.md)

## Pré-requisitos

- `git` e o binário `bb2gl` (compilado com Go 1.24+: `make build`; veja o [README](../README.md#instalação)).
- Acesso ao GitLab por **uma** destas formas (veja [Autenticação no GitLab](#autenticação-no-gitlab)):
  - `GITLAB_TOKEN` no `.env` ou no ambiente: personal access token com escopo `api`. Não precisa do `glab`.
  - [`glab`](https://gitlab.com/gitlab-org/cli) autenticado (`glab auth login`). É usado quando `GITLAB_TOKEN` não está definido.

  Nos dois casos, a conta precisa de papel **Maintainer** no grupo de destino (**Owner** para `--retry-failed-imports`, que apaga projetos).
- Um **API token (classic)** do Bitbucket Cloud com os escopos `read:repository:bitbucket` e `read:pullrequest:bitbucket`. App Passwords não são usados: o Bitbucket os desativou em 2026-06-09.
- **GitLab 18.9 ou mais recente**: o importador só aceita API token do Bitbucket a partir dessa versão. No gitlab.com isso já é atendido; a exigência vale para instâncias self-managed (o bb2gl aponta para o gitlab.com; veja [Desenvolvimento](desenvolvimento.md#estrutura-do-código)).

## Arquivo `.env`

```bash
cp .env.example .env   # preencha e não commite
```

| Variável | Para quê |
|---|---|
| `BITBUCKET_USER` | **E-mail** da conta Atlassian. Usado na API do Bitbucket e enviado ao importador do GitLab. |
| `BITBUCKET_TOKEN` | API token do Bitbucket Cloud (`read:repository:bitbucket` + `read:pullrequest:bitbucket`). |
| `BITBUCKET_GIT_USER` | Opcional. Usuário só do `git clone`/`ls-remote`. Padrão: `x-bitbucket-api-token-auth`. |
| `GITLAB_TOKEN` | Opcional. Personal access token do GitLab com escopo `api`. Se definido, o bb2gl usa a API e o Git do GitLab direto, sem o `glab`. Vazio: usa o `glab`. |
| `GITLAB_DEST_PATH` | Grupo/subgrupo base, no formato `grupo/subgrupo` (sem barra inicial; uma barra inicial é removida). Vazio: o bb2gl pergunta. |

Variáveis já exportadas no ambiente têm precedência sobre o `.env`. O `.env` lido por padrão é o do diretório atual; para usar outro arquivo, passe `--env-file ARQ`.

### Quais credenciais cada etapa usa

| Etapa | Credencial | Validada antes da migração |
|---|---|---|
| Verificação da origem (`git ls-remote`) e clone | `BITBUCKET_GIT_USER` + `BITBUCKET_TOKEN` | Sim, em todos os repositórios da lista. |
| Importador do GitLab | `BITBUCKET_USER` (e-mail) + `BITBUCKET_TOKEN`, enviados ao GitLab na chamada `POST /import/bitbucket` | Sim, quando há projetos a importar: formato de e-mail, acesso ao repositório e a pull requests. Se falhar, o bb2gl **para antes da confirmação** (código 2). Com `--allow-git-only`, segue sem PRs; `--reimport` e `--retry-failed-imports` exigem o importador sempre. |
| Consultas, importação, criação de projeto e push no GitLab | `GITLAB_TOKEN`, se definido; senão o token do `glab` | Sim (`GET /user`). O cabeçalho da execução mostra qual foi usado: `(GITLAB_TOKEN)` ou `(glab)`. |

## Autenticação no GitLab

O bb2gl escolhe a forma pela presença de `GITLAB_TOKEN`: com ele, fala direto com a API (`PRIVATE-TOKEN`) e autentica o Git por HTTPS com o mesmo token. Sem ele, usa o `glab` para a API e o credential helper do `glab` para o Git. A conta e as permissões exigidas são as mesmas.

### Opção A: token via variável de ambiente (sem `glab`)

1. No gitlab.com, clique no avatar → **Edit profile** → **Access tokens** → **Add new token**.
2. Dê um nome (ex.: `migracao-bitbucket`), uma data de expiração e marque o escopo **`api`**. Ele cobre a API (importação, criação/rename/exclusão de projetos) e o Git por HTTPS (clone, `ls-remote`, push).
3. Copie o token (`glpat-...`) e coloque no `.env`:

   ```bash
   GITLAB_TOKEN=glpat-xxxxxxxxxxxxxxxxxxxx
   ```

   Ou exporte só na sessão, sem gravar em arquivo:

   ```bash
   export GITLAB_TOKEN=glpat-xxxxxxxxxxxxxxxxxxxx
   bb2gl --dry-run
   ```

Os PRs cujo autor não estiver ligado a um usuário do GitLab ficam no nome da conta dona do token. Por isso, recomenda-se gerar o token numa **conta de serviço dedicada à migração** (ex.: `migracao-bitbucket`), e não na conta pessoal de quem roda o bb2gl (veja [Autoria dos PRs](autoria-prs.md#quem-roda-a-importação-use-uma-conta-dedicada)).

O token não aparece em `ps`, na URL do remoto, no `.git/config` nem no log: na API ele vai no cabeçalho HTTP, e no Git vai por `http.extraheader` passado por variável de ambiente.

### Opção B: `glab`

1. Instale o [`glab`](https://gitlab.com/gitlab-org/cli#installation) (ex.: `brew install glab`, `sudo apt install glab` ou o binário da página de releases).
2. Autentique:

   ```bash
   glab auth login
   ```

   Responda:
   - **GitLab instance**: `gitlab.com`;
   - **Login method**: `Web` (abre o navegador) ou `Token` (cole um personal access token com escopos `api` e `write_repository`);
   - **Default git protocol**: `HTTPS`;
   - **Authenticate Git with your GitLab credentials?**: `Yes`. Isso configura o credential helper do `glab`, que o bb2gl usa no clone, no `ls-remote` e no push. Sem ele, o Git pede usuário/senha e a etapa falha.
3. Confira:

   ```bash
   glab auth status                 # deve mostrar "Logged in to gitlab.com"
   glab api user | head             # deve mostrar o seu usuário
   ```

Deixe `GITLAB_TOKEN` vazio ou ausente para que o `glab` seja usado. Se `GITLAB_TOKEN` estiver definido (no `.env` ou exportado no shell), ele tem prioridade.

## Lista de repositórios (`repos.txt`)

Uma linha por repositório. `#` inicia comentário. Para usar outro arquivo, passe o caminho como argumento: `bb2gl outra-lista.txt`.

```bash
cp repos.txt.example repos.txt   # exemplo com nomes fictícios; o repos.txt não é commitado
```

```
acme-workspace/loja-api                                      # destino: <GITLAB_DEST_PATH>/loja-api
acme-workspace/app-mobile  acme-group/mobile/app-mobile      # destino explícito (ignora GITLAB_DEST_PATH)
acme-workspace/legado-erp -> acme-group/legado/erp           # "->" entre os campos também é aceito
```

Antes de qualquer alteração o bb2gl rejeita destinos inválidos, destinos duplicados (sem diferenciar maiúsculas, como o GitLab) e nomes que não servem para projeto novo.

Para descobrir repositórios do workspace que ainda não estão na lista, veja [Buscar repositórios fora da lista](busca-repositorios.md).
