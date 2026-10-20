package gitlab

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/1roody/bb2glab-migrate/internal/terminal"
)

// API: uma chamada à API REST v4 do GitLab (endpoint relativo a /api/v4/). Em erro devolve
// *APIError, com a mensagem no formato do GitLab (ex.: "404 Project Not Found").
type API interface {
	Do(method, endpoint string, body map[string]any) (json.RawMessage, error)
}

type APIError struct {
	Status  int // 0 quando desconhecido (glab, falha de rede)
	Message string
}

func (e *APIError) Error() string { return e.Message }

func IsNotFound(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == 404 {
		return true
	}
	return err != nil && strings.HasPrefix(strings.TrimSpace(err.Error()), "404")
}

// ErrorText: texto curto do erro para a tela e o log.
func ErrorText(err error) string {
	if err == nil {
		return "erro desconhecido"
	}
	msg := err.Error()
	if msg == "" {
		msg = "erro desconhecido"
	}
	return terminal.Truncate(msg, 300)
}

// errorMessageFromBody extrai "message" ou "error" de um corpo JSON do GitLab ("" se não houver).
func errorMessageFromBody(raw []byte) string {
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return ""
	}
	for _, key := range []string{"message", "error"} {
		switch v := data[key].(type) {
		case nil:
		case string:
			if v != "" {
				return v
			}
		default:
			b, _ := json.Marshal(v)
			return string(b)
		}
	}
	return ""
}

func orEmptyObject(raw []byte) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage("{}")
	}
	return raw
}

// TokenAPI fala direto com a API usando um personal access token (GITLAB_TOKEN).
type TokenAPI struct {
	BaseURL string // https://gitlab.com
	Token   string
	Client  *http.Client
}

func NewTokenAPI(host, token string) *TokenAPI {
	return &TokenAPI{BaseURL: "https://" + host, Token: token, Client: &http.Client{Timeout: 60 * time.Second}}
}

func (t *TokenAPI) Do(method, endpoint string, body map[string]any) (json.RawMessage, error) {
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, &APIError{Message: err.Error()}
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, t.BaseURL+"/api/v4/"+endpoint, payload)
	if err != nil {
		return nil, &APIError{Message: err.Error()}
	}
	req.Header.Set("PRIVATE-TOKEN", t.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := t.Client.Do(req)
	if err != nil {
		return nil, &APIError{Message: fmt.Sprintf("falha de rede: %v", err)}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 400 {
		return orEmptyObject(raw), nil
	}
	msg := errorMessageFromBody(raw)
	if msg == "" {
		msg = resp.Status // mesmo formato do GitLab ("404 Not Found"), usado por State
	}
	return nil, &APIError{Status: resp.StatusCode, Message: msg}
}

// GlabAPI usa o `glab api`, com o token do `glab auth login`.
type GlabAPI struct {
	Host string
}

func (g *GlabAPI) Do(method, endpoint string, body map[string]any) (json.RawMessage, error) {
	args := []string{"api", "--hostname", g.Host, "-X", method, endpoint}
	stdin := ""
	if body != nil {
		// Corpo JSON pela entrada padrão: segredos (token do importador) não aparecem em `ps`.
		b, err := json.Marshal(body)
		if err != nil {
			return nil, &APIError{Message: err.Error()}
		}
		args = append(args, "-H", "Content-Type: application/json", "--input", "-")
		stdin = string(b)
	}
	cmd := exec.Command("glab", args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err == nil {
		return orEmptyObject(stdout.Bytes()), nil
	} else if stderr.Len() == 0 {
		stderr.WriteString(err.Error())
	}
	msg := errorMessageFromBody(stdout.Bytes())
	if msg == "" {
		msg = strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
	}
	return nil, &APIError{Message: msg}
}
