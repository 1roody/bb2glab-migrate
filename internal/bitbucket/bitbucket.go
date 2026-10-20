// Package bitbucket é o cliente da API do Bitbucket Cloud: verificação de credenciais,
// listagem de repositórios e a URL de clone autenticada.
package bitbucket

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/1roody/bb2glab-migrate/internal/git"
)

const (
	DefaultAPI     = "https://api.bitbucket.org/2.0/repositories"
	DefaultGitBase = "https://bitbucket.org"
	DefaultGitUser = "x-bitbucket-api-token-auth"
)

// Client: API REST e importador usam o e-mail da conta + API token. No Git HTTPS o
// Bitbucket recusa o e-mail com API token, então usa-se outro usuário (padrão
// x-bitbucket-api-token-auth).
type Client struct {
	User, Token, GitUser string
	APIBase              string // {APIBase}/{workspace}/{repo}
	GitBase              string // {GitBase}/{workspace}/{repo}.git
	Client               *http.Client
}

func New(user, token, gitUser string) *Client {
	if gitUser == "" {
		gitUser = DefaultGitUser
	}
	return &Client{User: user, Token: token, GitUser: gitUser,
		APIBase: DefaultAPI, GitBase: DefaultGitBase, Client: &http.Client{Timeout: 60 * time.Second}}
}

func (b *Client) GitAuthEnv() []string { return git.AuthHeaderEnv(b.GitUser, b.Token) }

func (b *Client) CloneURL(src string) string { return b.GitBase + "/" + src + ".git" }

func (b *Client) get(rawURL string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(b.User+":"+b.Token)))
	return b.Client.Do(req)
}

// APIStatus: status HTTP de um GET em {APIBase}/{path} (0 = falha de rede).
func (b *Client) APIStatus(path string) int {
	resp, err := b.get(b.APIBase + "/" + path)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

type Repo struct {
	FullName  string `json:"full_name"`
	Name      string `json:"name"`
	IsPrivate bool   `json:"is_private"`
	UpdatedOn string `json:"updated_on"`
}

// ListRepos lista todos os repositórios de um workspace (paginado).
func (b *Client) ListRepos(workspace string) ([]Repo, error) {
	qs := url.Values{
		"pagelen": {"100"},
		"fields":  {"next,values.full_name,values.name,values.is_private,values.updated_on"},
	}
	next := b.APIBase + "/" + workspace + "?" + qs.Encode()
	var out []Repo
	for next != "" {
		resp, err := b.get(next)
		if err != nil {
			return nil, fmt.Errorf("falha de rede ao listar %s: %v", workspace, err)
		}
		var page struct {
			Next   string `json:"next"`
			Values []Repo `json:"values"`
		}
		status := resp.StatusCode
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if status >= 400 {
			return nil, fmt.Errorf("HTTP %d ao listar %s", status, workspace)
		}
		if err != nil {
			return nil, fmt.Errorf("falha de rede ao listar %s: %v", workspace, err)
		}
		out = append(out, page.Values...)
		next = page.Next
	}
	return out, nil
}

// CheckImporterCredentials valida as credenciais que o importador do GitLab vai usar ("" se OK).
func (b *Client) CheckImporterCredentials(sampleSrc string) string {
	if !strings.Contains(b.User, "@") {
		return "BITBUCKET_USER precisa ser o e-mail da conta Atlassian (exigido pelo importador)"
	}
	if status := b.APIStatus(sampleSrc); status != 200 {
		return fmt.Sprintf("API do Bitbucket recusou e-mail/token (HTTP %d; escopo read:repository:bitbucket?)", status)
	}
	if status := b.APIStatus(sampleSrc + "/pullrequests?pagelen=1"); status != 200 {
		return fmt.Sprintf("API token sem acesso a pull requests (HTTP %d; escopo read:pullrequest:bitbucket?)", status)
	}
	return ""
}
