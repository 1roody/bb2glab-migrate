package terminal

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// ---- utilidades de texto
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func Width() int {
	cols := 0
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		cols = w
	} else if c, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil {
		cols = c
	}
	if cols < 40 { // alguns terminais/pty informam largura 0
		return 80
	}
	return cols
}

func Elapsed(t0 time.Time) string {
	s := time.Since(t0).Seconds()
	if s < 60 {
		return fmt.Sprintf("%.1fs", s)
	}
	return fmt.Sprintf("%dm%02ds", int(s)/60, int(s)%60)
}

func RuneLen(s string) int { return utf8.RuneCountInString(s) }

func Truncate(s string, n int) string {
	if n < 0 {
		n = 0
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// SplitLines: linhas do texto, sem os espaços em branco finais (como rstrip().splitlines()).
func SplitLines(s string) []string {
	s = strings.TrimRight(s, " \t\r\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}
