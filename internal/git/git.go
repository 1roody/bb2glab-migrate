// Package git roda o git (ls-remote, for-each-ref, clone, push) com a credencial em header,
// e compara refs entre origem e destino (refs.go).
package git

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/1roody/bb2glab-migrate/internal/terminal"
)

type Output struct {
	Stdout, Stderr string
	Code           int
}

func (r Output) OK() bool { return r.Code == 0 }

// runCommand executa o comando sem prompt de credenciais do Git; env complementa o ambiente atual.
func runCommand(dir string, env []string, stdin string, name string, args ...string) Output {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), env...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	res := Output{Stdout: out.String(), Stderr: errb.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		res.Code = exit.ExitCode()
	default:
		res.Code = -1
		res.Stderr += err.Error()
	}
	return res
}

func Run(dir string, env []string, args ...string) Output {
	return runCommand(dir, env, "", "git", args...)
}

// AuthHeaderEnv: credencial do Git HTTPS via header em variáveis de ambiente. Não aparece em
// `ps`, na URL, no .git/config nem no log.
func AuthHeaderEnv(user, token string) []string {
	auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraheader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + auth,
	}
}

// ListRemoteRefs: branches e tags do remoto (ref -> SHA). Em falha devolve erro com a última linha
// da saída: erro de rede ou autenticação nunca vira "repositório sem refs".
func ListRemoteRefs(url string, env []string) (Refs, error) {
	p := Run("", env, "ls-remote", "--heads", "--tags", url)
	if !p.OK() {
		out := p.Stderr
		if strings.TrimSpace(out) == "" {
			out = p.Stdout
		}
		if lines := terminal.SplitLines(strings.TrimSpace(out)); len(lines) > 0 {
			return nil, errors.New(lines[len(lines)-1])
		}
		return nil, fmt.Errorf("git ls-remote saiu com código %d", p.Code)
	}
	refs := Refs{}
	for _, line := range strings.Split(p.Stdout, "\n") {
		sha, ref, _ := strings.Cut(line, "\t")
		if ref != "" && !strings.HasSuffix(ref, "^{}") {
			refs[ref] = sha
		}
	}
	return refs, nil
}

func ListLocalRefs(repo string) Refs {
	p := Run(repo, nil, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads", "refs/tags")
	refs := Refs{}
	for _, line := range strings.Split(p.Stdout, "\n") {
		if sha, ref, ok := strings.Cut(line, " "); ok {
			refs[ref] = sha
		}
	}
	return refs
}
