# Autoria dos PRs: o que cada pessoa precisa fazer antes da importação

[← README](../README.md)

O importador liga cada autor ou comentarista do Bitbucket a um usuário do GitLab. Para isso, **cada pessoa que tem PRs ou comentários no Bitbucket** precisa fazer os três passos abaixo **antes** de o repositório ser importado. Isso vale também para o `--reimport`.

1. **Igualar o nome público da Atlassian ao usuário do Bitbucket**
   - Ver o usuário do Bitbucket em Bitbucket → ⚙ (Configurações) → **Personal Bitbucket settings** → **Account settings** → *Username*.
   - Ver o nome público em [id.atlassian.com](https://id.atlassian.com/manage-profile/profile-and-visibility) → **Profile and visibility** → *Public name*.
   - Se forem diferentes, altere o **Public name** para ficar igual ao usuário do Bitbucket.
2. **Conectar a conta do Bitbucket ao perfil do GitLab**
   - No gitlab.com, clique no avatar → **Edit profile** → **Account** → **Service sign-in** → **Connect** ao lado de **Bitbucket**, e autorize no Bitbucket.
   - Depois de conectar, não troque o usuário do Bitbucket. Se trocar, a ligação para de funcionar e é preciso desconectar e conectar de novo.
3. **Definir o e-mail público no perfil do GitLab**
   - No gitlab.com, clique no avatar → **Edit profile** → **Public email**, escolha o e-mail e clique em **Update profile settings**.

Se a ligação não existir no momento da importação, o PR ou comentário aparece como criado pelo usuário que rodou o bb2gl, com uma referência ao autor original no texto. **Isso não é corrigido depois.** Para refazer, só apagando o projeto e importando de novo.

## Quem roda a importação: use uma conta dedicada

Os PRs e comentários cujo autor não estiver ligado a um usuário do GitLab ficam no nome da conta que rodou o bb2gl. Por isso, recomenda-se rodar a migração com uma **conta de serviço dedicada** (por exemplo, um usuário `migracao-bitbucket`), e não com a conta pessoal de alguém do time: assim fica claro, no histórico, o que veio sem autor ligado.

- A conta precisa de papel **Maintainer** no grupo de destino (**Owner** para `--retry-failed-imports`).
- Crie um personal access token dessa conta com escopo `api` e use-o em `GITLAB_TOKEN` (veja [Configuração](configuracao.md#opção-a-token-via-variável-de-ambiente-sem-glab)).
- As credenciais do Bitbucket (`BITBUCKET_USER` e `BITBUCKET_TOKEN`) são independentes: só precisam ter acesso de leitura aos repositórios e aos PRs.

## Referências

- [Importador do Bitbucket Cloud](https://docs.gitlab.com/user/import/bitbucket_cloud/) (documentação do GitLab).
- [Guia de migração do Bitbucket Cloud do Congregate](https://gitlab.com/gitlab-org/professional-services-automation/tools/migration/congregate/-/blob/master/customer/bitbucket-cloud-migration-doc.md), a ferramenta de migração do GitLab Professional Services, que usa o mesmo importador.
