# Como a migração funciona

[← README](../README.md)

```bash
bb2gl --dry-run     # verifica tudo e mostra o plano; não cria nem envia nada
bb2gl               # migra de verdade (pede 'yes')
bb2gl --yes         # automação: confirma a lista revisada sem perguntar
```

Toda execução passa por três fases: [verificação](#1-verificação-somente-leitura), [confirmação](#2-confirmação) e [migração](#3-migração-importador--fallback-git).

## 1. Verificação (somente leitura)

Para cada repositório, o bb2gl consulta o projeto no GitLab e compara as refs (branches e tags) da origem e do destino:

| Situação | Ação |
|---|---|
| Destino não existe | **Importar via API** e, se preciso, completar refs por Git. O grupo de destino precisa existir. |
| Projeto vazio pré-existente | **Bloqueado**: o importador só cria projetos novos. Apague o projeto, use `--reimport` ou `--allow-git-only` (push Git, sem PRs). |
| Importação anterior falhou | **Bloqueado**. `--retry-failed-imports` apaga o projeto vazio e importa de novo. Se não estiver vazio: `--reimport`. |
| Destino tem parte das refs, sem divergência | **Completar refs**: envia só as que faltam, sem sobrescrever. |
| Destino tem todas as refs da origem | **Concluído**: ignorado, e a URL vai para `repos-ja-no-gitlab.txt`. Refs que só existem no destino não atrapalham. A tela mostra quantos foram migrados só por Git (sem PRs). |
| Alguma ref com SHA diferente | **Bloqueado** (falha). Mostra a diferença. Só é sobrescrito com `--force`. |
| Importação anterior ainda em andamento | **Bloqueado**. Rode de novo depois. |
| Origem sem nenhum branch | Ignorado. |
| Falha ao consultar GitLab ou Bitbucket (autenticação, rede, rate limit, indisponibilidade) | **Bloqueado** (falha). Nunca é tratado como "não existe" ou "vazio". |

Ter "algum commit" no destino não basta para considerar a migração concluída.

## 2. Confirmação

- Se sobrar algo para migrar, a tela de confirmação lista só esses repositórios, com a ação de cada um. Responda `yes` para confirmar ou `no` para cancelar sem alterar nada.
- Em automação (sem terminal), é obrigatório passar `--yes`. `--yes` nunca implica `--force`.
- Se não sobrar nada, o bb2gl encerra com uma mensagem clara: código `0` quando tudo já está completo, código `1` se houver bloqueados.

## 3. Migração (importador → fallback Git)

### Destino novo

1. Chama `POST /api/v4/import/bitbucket` e acompanha `import_status` até `finished`, `failed` ou o fim do prazo (`--import-timeout`, padrão 1800s). A resposta `202` não é tratada como importação concluída.
2. O projeto precisa aparecer exatamente no caminho esperado. Se aparecer em outro lugar, é falha.
3. Se a importação for recusada ou falhar, o repositório termina como **falha** e nada é enviado por Git. Se a tentativa deixou um projeto vazio, a próxima execução o bloqueia e sugere `--retry-failed-imports`.
4. Com `--allow-git-only`, em vez disso, o projeto deixado pela tentativa é reutilizado (ou é criado um vazio e privado) e o conteúdo vai por Git, sem PRs.
5. Se a importação não terminar no prazo, **nada é enviado por Git**, para não disputar com o importador. Rode de novo mais tarde.

### `--retry-failed-imports`

Só apaga um projeto que continua vazio, foi criado pelo importador do Bitbucket e tem a importação marcada como falha. Se o gitlab.com só marcar o projeto para exclusão (exclusão adiada), o bb2gl tenta a remoção definitiva. Se o caminho continuar ocupado, avisa para tentar mais tarde.

### `--reimport` (projetos migrados sem o importador, sem PRs)

1. Renomeia o projeto atual para `<nome>-pre-reimport-<id>` e o arquiva. Nada é apagado.
2. Importa com PRs no caminho original.
3. Completa por Git as refs da origem que faltarem.
4. Reenvia ao projeto novo o que só existia no GitLab:
   - branches criadas lá;
   - avanços feitos lá (fast-forward);
   - branches que divergiram da origem, que são preservadas como `gitlab-pre-reimport/<branch>`.
5. Se a importação for recusada antes de criar o projeto novo, desfaz o rename e o arquivamento.

Projetos que já vieram do importador (com PRs) não são reimportados. Configurações, membros, branches protegidas, variáveis de CI, integrações, MRs e issues criados no GitLab ficam só no projeto arquivado e precisam ser reaplicados no projeto novo. O ID do projeto muda; a URL continua a mesma.

Antes de reimportar, confira a [autoria dos PRs](autoria-prs.md): ela vale também para o `--reimport`.

### Fallback Git (todas as ações)

1. Compara de novo as refs logo antes de enviar.
2. Clona com `--mirror` e envia só as refs que faltam, com `ci.skip` para não disparar pipelines.
3. Refs divergentes só são sobrescritas com `--force`, e só elas (`+ref:ref`).
4. Em destino que estava vazio, ajusta a branch padrão igual à do Bitbucket.

### Verificação final

Todas as refs da origem precisam estar no destino, com o mesmo SHA. Se não estiverem, ou se o destino não puder ser consultado, o repositório termina como falha.

## Resultado de cada repositório

| Estado | Significado |
|---|---|
| ✓ **completo** | Importador concluiu sem falhas e o Git foi verificado sem precisar de push. Metadados trazidos pelo importador. |
| ◐ **parcial** | Git verificado, mas metadados **não garantidos**: o importador teve relações com falha, o Git precisou ser completado, ou a migração foi feita sem PRs com `--allow-git-only`. |
| ✗ **falha** | Refs ausentes ou divergentes, push recusado, destino não verificado ou importação sem terminar no prazo. |
| ⊘ **bloqueado** | Não foi processado: divergente, não verificado, importação em andamento, importação anterior que falhou ou projeto vazio pré-existente. |
| ↷ **ignorado** | Já completo ou vazio na origem. |

O resumo mostra, para cada parcial e cada falha, o resultado da API, do Git e dos metadados separadamente.

É seguro rodar de novo: o que está completo é ignorado, o que está incompleto é retomado e o importador não é chamado de novo para projetos que já existem.

## Arquivo de resultados (`results/`)

Ao final de toda execução que passa da verificação (inclusive `--dry-run`, cancelamento e parada por importador indisponível), o bb2gl grava em `results/` dois arquivos com o mesmo nome do log:

- `bb2gl-AAAAMMDD-HHMMSS.json`: um bloco `execucao` (data, lista, `dry_run`, usuário do GitLab, código de saída) e a lista `repositorios`;
- `bb2gl-AAAAMMDD-HHMMSS.csv`: as mesmas linhas, para abrir numa planilha.

Há uma linha por repositório da lista, para que cada time confira e aprove a sua migração:

| Campo | Conteúdo |
|---|---|
| `origem` / `destino` | `workspace/repo` no Bitbucket e `grupo/.../projeto` no GitLab. |
| `acao` | Ação decidida na verificação (`importar via API`, `completar refs`, `concluído`, `projeto vazio pré-existente`...). |
| `estado` | `completo`, `parcial`, `falha`, `bloqueado`, `ignorado` ou `pendente` (estava para migrar, mas não foi nesta execução: dry-run, cancelado, interrompido ou importador indisponível). |
| `url` | URL do projeto no GitLab, quando ele existe. |
| `motivo` | Por que terminou nesse estado (vazio quando completo). |
| `api`, `git`, `metadados` | Resultado de cada etapa, como no resumo da tela. |
| `avisos` | Avisos da migração (LFS, `bitbucket-pipelines.yml`, divergências preservadas...). No CSV, separados por ` \| `. |
| `projeto_anterior` | `--reimport`: caminho do projeto arquivado. |

Os arquivos não são sobrescritos: cada execução grava os seus. Para usar outro diretório, passe `--results-dir DIR`.

## O que cada etapa migra

| Dado | Importador (API) | Fallback Git |
|---|---|---|
| Branches, tags, histórico | Sim | Sim |
| Descrição do repositório | Sim | Não |
| Pull requests e comentários | Sim (imagens/anexos ficam como links externos; PRs de forks chegam vazios) | **Não** |
| Objetos Git LFS | Sim | **Não** (só os ponteiros) |
| Issues, wiki, aprovações de PR, webhooks, membros, variáveis de pipeline | Não | Não |
| `bitbucket-pipelines.yml` → `.gitlab-ci.yml` | Não | Não |

O fallback Git não recupera os metadados que o importador deixou de trazer. Nesses casos o resultado é **parcial**, nunca "migração completa".

## Opções

| Opção | Efeito |
|---|---|
| `[repos.txt]` (posicional) | Arquivo com a lista de repositórios, antes ou depois das opções. Padrão: `repos.txt`. |
| `-n`, `--dry-run` | Só verifica e mostra o plano. |
| `-y`, `--yes` | Confirma sem perguntar (automação). Não implica `--force`. |
| `-f`, `--force` | Sobrescreve refs divergentes no destino. Aparece em vermelho no plano e no resumo. |
| `--allow-git-only` | Aceita migrar sem PRs: projeto vazio + push Git quando o importador falha ou não pode ser usado, e push em projeto vazio pré-existente. |
| `--retry-failed-imports` | Apaga o projeto vazio deixado por uma importação que falhou e importa de novo. Exige papel Owner. |
| `--reimport` | Arquiva os projetos migrados sem o importador e os importa de novo com PRs, preservando o trabalho feito no GitLab. |
| `--import-timeout SEG` | Espera máxima por cada importação via API. Padrão: 1800. |
| `--env-file ARQ` | Outro `.env`. Padrão: `.env` no diretório atual. |
| `--workdir DIR` | Onde ficam os clones temporários. Padrão: diretório temporário. |
| `--log ARQ` | Arquivo de log. Padrão: `logs/bb2gl-AAAAMMDD-HHMMSS.log`. |
| `--results-dir DIR` | Onde gravar o [resultado por repositório](#arquivo-de-resultados-results) (JSON e CSV). Padrão: `results`. |
| `--existing-out ARQ` | URLs no GitLab dos repositórios já completos, uma por linha. O arquivo é regravado a cada execução. Padrão: `repos-ja-no-gitlab.txt`. |

As opções de busca (`--search`, `--missing`, `--workspace`, `--add`) estão em [Buscar repositórios fora da lista](busca-repositorios.md). A lista completa também sai em `bb2gl --help`.

## Saída na tela e log

- **Tela:** colorida e resumida (✓ ok, ⚠ aviso, ✗ falha, ◐ parcial, ⊘ bloqueado, ↷ ignorado), com indicador de progresso e tempo de cada etapa. A saída do git só aparece em falhas (últimas linhas). Fica sem cores se a saída não for um terminal ou se `NO_COLOR` estiver definido.
- **Arquivo de log** (`--log`): texto puro com horário. Contém tudo o que saiu na tela e também a saída bruta (raw) de cada comando `git`, as mudanças de `import_status` e as relações que o importador não conseguiu trazer. É criado quando há migração ou dry-run.

## Código de saída

| Código | Significado |
|---|---|
| `0` | Tudo completo (ou nada a migrar). |
| `1` | Alguma falha ou bloqueio. |
| `2` | Erro de uso ou pré-requisito. |
| `3` | Só parciais (Git verificado, metadados não garantidos). |
| `130` | Cancelado. |
