// Package terminal é a saída do bb2gl: tela colorida e enxuta, spinner e arquivo de log
// em texto puro com horário.
package terminal

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ColorFunc func(string) string

// UI: tela colorida e enxuta (a saída dos comandos só aparece em falhas, últimas linhas) e
// arquivo de log em texto puro com horário, incluindo a saída bruta de cada comando.
//
// Até OpenLog as linhas ficam em memória: o arquivo só é criado se houver migração (ou
// dry-run), mas registra também a verificação e o plano confirmado.
type UI struct {
	out, errw io.Writer
	tty       bool
	color     bool
	mu        sync.Mutex
	path      string
	fh        *os.File
	pending   []string
}

func NewUI(out, errw io.Writer, tty bool) *UI {
	_, noColor := os.LookupEnv("NO_COLOR")
	color := tty && !noColor && os.Getenv("TERM") != "dumb"
	return &UI{out: out, errw: errw, tty: tty, color: color}
}

func (u *UI) sgr(code, text string) string {
	if !u.color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (u *UI) Bold(s string) string { return u.sgr("1", s) }

func (u *UI) Dim(s string) string { return u.sgr("2", s) }

func (u *UI) Green(s string) string { return u.sgr("32", s) }

func (u *UI) Yellow(s string) string { return u.sgr("33", s) }

func (u *UI) Red(s string) string { return u.sgr("31", s) }

func (u *UI) Cyan(s string) string { return u.sgr("36", s) }

func (u *UI) Blue(s string) string { return u.sgr("34", s) }

func (u *UI) Magenta(s string) string { return u.sgr("35", s) }

// ---- arquivo de log

// Log escreve só no arquivo de log (texto puro, uma linha com horário por linha de texto).
func (u *UI) Log(text string) {
	stamp := time.Now().Format("15:04:05")
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		lines = append(lines, strings.TrimRight(stamp+" "+ansiRE.ReplaceAllString(line, ""), " \t"))
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.fh != nil {
		fmt.Fprintln(u.fh, strings.Join(lines, "\n"))
	} else {
		u.pending = append(u.pending, lines...)
	}
}

func (u *UI) OpenLog(path string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.path, u.fh = path, fh
	for _, line := range u.pending {
		fmt.Fprintln(fh, line)
	}
	u.pending = nil
	return nil
}

// Path: caminho do arquivo de log ("" até OpenLog).
func (u *UI) Path() string { return u.path }

func (u *UI) Close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.fh != nil {
		u.fh.Close()
		u.fh = nil
	}
}

// LogCommand registra o comando executado e a sua saída bruta: só no log.
func (u *UI) LogCommand(cmd []string, output string) {
	var b strings.Builder
	b.WriteString("    $ " + strings.Join(cmd, " "))
	for _, line := range SplitLines(output) {
		b.WriteString("\n    | " + line)
	}
	u.Log(b.String())
}

// ---- tela + arquivo

// Print escreve na tela (stdout) e no log.
func (u *UI) Print(msg string) {
	u.mu.Lock()
	fmt.Fprintln(u.out, msg)
	u.mu.Unlock()
	u.Log(msg)
}

// PrintErr escreve em stderr e no log.
func (u *UI) PrintErr(msg string) {
	u.mu.Lock()
	fmt.Fprintln(u.errw, msg)
	u.mu.Unlock()
	u.Log(msg)
}

// PrintScreenOnly escreve só na tela (sem log).
func (u *UI) PrintScreenOnly(msg string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Fprintln(u.out, msg)
}

func (u *UI) Section(title string) {
	width := min(Width(), 100)
	u.Print("")
	u.Print(u.sgr("1;36", "━━ "+title+" ") + u.Cyan(strings.Repeat("━", max(width-RuneLen(title)-4, 4))))
}

func (u *UI) OK(msg, indent string) { u.Print(indent + u.Green("✓") + " " + msg) }

func (u *UI) Warn(msg, indent string) { u.Print(indent + u.Yellow("⚠") + " " + u.Yellow(msg)) }

func (u *UI) Fail(msg, indent string) { u.Print(indent + u.Red("✗") + " " + u.Red(msg)) }

func (u *UI) Error(msg string) { u.PrintErr(u.Red("✗ erro:") + " " + msg) }

// PrintOutputTail mostra na tela, em falhas, as últimas linhas da saída bruta (a completa já está no log).
func (u *UI) PrintOutputTail(output, indent string) {
	lines := SplitLines(output)
	if len(lines) > 12 {
		lines = lines[len(lines)-12:]
	}
	for _, line := range lines {
		u.PrintScreenOnly(indent + u.Dim("│ "+line))
	}
}
