# Problemas frequentes

[← README](../README.md)

| Sintoma | Causa provável |
|---|---|
| `o Bitbucket recusou BITBUCKET_USER/BITBUCKET_TOKEN (HTTP 401)` | Token inválido, expirado, revogado ou criado por outra conta que não a do e-mail em `BITBUCKET_USER`. Gere outro API token com `read:repository:bitbucket` e `read:pullrequest:bitbucket`. |
| `não verificados` com `Bitbucket: ...` | Token inválido, sem `read:repository:bitbucket`, nome errado ou sem acesso. Confira com `--search`. |
| `não verificados` com `GitLab: 401/403/429/5xx` | `GITLAB_TOKEN` ou token do `glab` inválido, sem permissão, rate limit ou GitLab indisponível. Rode de novo. |
| `grupo de destino ... não encontrado` | Crie o grupo no GitLab ou corrija `GITLAB_DEST_PATH`. |
| `importador indisponível: BITBUCKET_USER precisa ser o e-mail` | Use o e-mail da conta Atlassian em `BITBUCKET_USER`. |
| `importador indisponível: API token sem acesso a pull requests` | Gere um API token com os escopos `read:repository:bitbucket` e `read:pullrequest:bitbucket`. |
| `projeto vazio pré-existente` | O importador só cria projetos novos: apague o projeto vazio e rode de novo, ou use `--reimport`. |
| `importação anterior falhou` | Veja o motivo na tela/log e rode com `--retry-failed-imports`. |
| `N deles foram migrados só por Git (sem PRs)` | Use `--reimport` para trazer os PRs. |
| `refs divergentes` | O destino tem commits que a origem não tem (por exemplo, trabalho feito no GitLab depois da migração). Confira antes de usar `--force`. |
| `importação ... não terminou` | O importador ainda está rodando. Rode de novo depois (o repositório fica bloqueado até terminar). |
| `glab não está autenticado` | Rode `glab auth login` ou defina `GITLAB_TOKEN`. |
| `comando obrigatório não encontrado: glab` | Instale o `glab` ou defina `GITLAB_TOKEN` (dispensa o `glab`). |
| `o GitLab recusou GITLAB_TOKEN` | Token inválido, expirado, revogado ou sem o escopo `api`. Gere outro em **Edit profile → Access tokens**. |
| `GitLab (git ls-remote): ... could not read Username` (modo `glab`) | O Git não tem credencial do GitLab: rode `glab auth login` respondendo `Yes` em *Authenticate Git with your GitLab credentials?*, ou use `GITLAB_TOKEN`. |
| `destinos duplicados na lista` | Dois repositórios apontam para o mesmo destino. |
| `vazios no Bitbucket` | O repositório de origem não tem nenhum branch; não há o que migrar. |

Para investigar qualquer outro erro, abra o arquivo de log da execução (`logs/bb2gl-*.log`): ele tem a saída bruta de cada comando `git` e as mudanças de `import_status`.
