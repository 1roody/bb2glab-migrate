package gitlab

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// ---- GITLAB_TOKEN: API REST direta e Git autenticado por header, sem o glab
func tokenServer(t *testing.T, status int, body string, seen *[]*http.Request, bodies *[]string) *TokenAPI {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*seen = append(*seen, r)
		*bodies = append(*bodies, string(b))
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return &TokenAPI{BaseURL: srv.URL, Token: "glpat-segredo", Client: srv.Client()}
}

func TestTokenAPIDiretaComPrivateTokenECorpoJSON(t *testing.T) {
	var reqs []*http.Request
	var bodies []string
	api := tokenServer(t, 200, `{"id": 7}`, &reqs, &bodies)
	gl := &Client{API: api, Host: DefaultHost, Token: api.Token}
	var p Project
	if err := gl.Request("PUT", "projects/7", map[string]any{"default_branch": "main"}, &p); err != nil || p.ID != 7 {
		t.Fatalf("err=%v p=%+v", err, p)
	}
	r := reqs[0]
	if r.Method != "PUT" || r.URL.Path != "/api/v4/projects/7" || r.Header.Get("PRIVATE-TOKEN") != "glpat-segredo" {
		t.Fatalf("requisição: %s %s %v", r.Method, r.URL, r.Header)
	}
	var got map[string]any
	json.Unmarshal([]byte(bodies[0]), &got)
	if !reflect.DeepEqual(got, map[string]any{"default_branch": "main"}) {
		t.Fatalf("corpo = %s", bodies[0])
	}
}

func TestTokenAPICaminhoCodificado(t *testing.T) {
	var reqs []*http.Request
	var bodies []string
	api := tokenServer(t, 200, `{"id": 1, "path_with_namespace": "g/p"}`, &reqs, &bodies)
	gl := &Client{API: api, Host: DefaultHost}
	if state, _, _ := gl.FindProject("g/p"); state != Found {
		t.Fatalf("state = %v", state)
	}
	if reqs[0].RequestURI != "/api/v4/projects/g%2Fp" {
		t.Fatalf("uri = %s", reqs[0].RequestURI)
	}
}

func TestTokenAPI404ViraProjetoInexistente(t *testing.T) {
	var reqs []*http.Request
	var bodies []string
	gl := &Client{API: tokenServer(t, 404, `{"message": "404 Project Not Found"}`, &reqs, &bodies)}
	if state, _, msg := gl.FindProject("g/p"); state != NotFound || msg != "" {
		t.Fatalf("state=%v msg=%q", state, msg)
	}
}

func TestTokenAPIErroSemCorpoNuncaViraInexistente(t *testing.T) {
	var reqs []*http.Request
	var bodies []string
	gl := &Client{API: tokenServer(t, 503, "", &reqs, &bodies)}
	state, _, msg := gl.FindProject("g/p")
	if state != LookupFailed || !strings.Contains(msg, "503") {
		t.Fatalf("state=%v msg=%q", state, msg)
	}
}

func TestGitAutenticadoPorHeader(t *testing.T) {
	env := (&Client{Token: "glpat-segredo"}).GitAuthEnv()
	if env[1] != "GIT_CONFIG_KEY_0=http.extraheader" ||
		!strings.Contains(env[2], base64.StdEncoding.EncodeToString([]byte("oauth2:glpat-segredo"))) {
		t.Fatalf("env = %v", env)
	}
	if (&Client{}).GitAuthEnv() != nil { // sem token: credential helper do glab
		t.Fatal("sem token não devia haver header")
	}
}
