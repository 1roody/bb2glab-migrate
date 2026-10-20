# Buscar repositórios fora da lista

[← README](../README.md)

Varre o **workspace inteiro** no Bitbucket e compara com o `repos.txt`. É somente leitura, não precisa do `glab` nem do `GITLAB_TOKEN` e não migra nada.

```bash
bb2gl --search termo            # procura por nome (aceita vários --search)
bb2gl --missing                 # lista tudo o que está no workspace e não está no repos.txt
bb2gl --search termo --add      # acrescenta ao repos.txt o que faltar (pede 'yes')
```

Cada resultado vem marcado como `[na lista]` ou `[FORA lista]`. Termos sem resultado aparecem como `[sem resultado]`.

| Opção | Efeito |
|---|---|
| `--search TERMO` | Filtra por nome ou slug (contém, sem diferenciar maiúsculas). |
| `--missing` | Mostra só os que não estão na lista. |
| `--workspace WS` | Workspace a varrer. Padrão: os que aparecem no `repos.txt`. |
| `--add` | Grava os que faltam no `repos.txt` como `workspace/repo`, um por linha. |

Limites:
- O Bitbucket Cloud não lista workspaces nem repositórios entre workspaces (a API global retorna 404). Para outro workspace, use `--workspace`.
- Só aparecem os repositórios que a sua conta enxerga.
