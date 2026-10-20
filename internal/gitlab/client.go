// Package gitlab é o cliente da API REST v4 do GitLab, direto com GITLAB_TOKEN (api.go,
// TokenAPI) ou pelo `glab` (GlabAPI), e as consultas usadas pela migração (client.go).
package gitlab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/1roody/bb2glab-migrate/internal/git"
)

const DefaultHost = "gitlab.com"

// Client: operações usadas pela migração sobre a API (GITLAB_TOKEN ou glab).
type Client struct {
	API   API
	Host  string
	Token string // GITLAB_TOKEN; vazio: Git usa o credential helper configurado pelo glab
}

type Lookup int

const (
	Found Lookup = iota
	NotFound
	LookupFailed
)

// EscapePath codifica um caminho como um único segmento da URL ("g/p" -> "g%2Fp").
func EscapePath(s string) string { return url.PathEscape(s) }

func (g *Client) Request(method, endpoint string, body map[string]any, out any) error {
	raw, err := g.API.Do(method, endpoint, body)
	if err != nil {
		return err
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return &APIError{Message: "resposta inválida da API: " + err.Error()}
		}
	}
	return nil
}

// FindProject: (exists, projeto) | (missing) se 404 | (error, motivo) para autenticação, rede,
// rate limit ou indisponibilidade, nunca tratado como inexistente.
func (g *Client) FindProject(path string) (Lookup, Project, string) {
	var p Project
	err := g.Request(http.MethodGet, "projects/"+EscapePath(path), nil, &p)
	if err == nil && p.ID != 0 {
		// A API segue o redirecionamento de projetos renomeados: se respondeu outro caminho,
		// este está livre (ex.: depois do rename feito pelo --reimport).
		got := p.PathWithNamespace
		if got == "" {
			got = path
		}
		if !strings.EqualFold(got, path) {
			return NotFound, Project{}, ""
		}
		return Found, p, ""
	}
	if err == nil {
		return LookupFailed, Project{}, "resposta sem id do projeto"
	}
	if IsNotFound(err) {
		return NotFound, Project{}, ""
	}
	return LookupFailed, Project{}, ErrorText(err)
}

func (g *Client) NamespaceID(path string) (int, string) {
	var ns struct {
		ID int `json:"id"`
	}
	err := g.Request(http.MethodGet, "namespaces/"+EscapePath(path), nil, &ns)
	if err == nil && ns.ID != 0 {
		return ns.ID, ""
	}
	return 0, ErrorText(err)
}

// GitAuthEnv: credencial do Git HTTPS no GitLab. Com GITLAB_TOKEN, header Basic oauth2:<token>;
// sem ele, o credential helper configurado pelo `glab auth login`.
func (g *Client) GitAuthEnv() []string {
	if g.Token == "" {
		return nil
	}
	return git.AuthHeaderEnv("oauth2", g.Token)
}

func (g *Client) RenameProject(p Project, path, name string) (Project, error) {
	var out Project
	err := g.Request(http.MethodPut, fmt.Sprintf("projects/%d", p.ID), map[string]any{"path": path, "name": name}, &out)
	return out, err
}
