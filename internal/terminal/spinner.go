package terminal

import (
	"fmt"
	"sync"
	"time"
)

const spinFrames = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"

// Spinner: indicador animado só na tela (terminal interativo); o texto pode mudar com Set.
type Spinner struct {
	ui     *UI
	indent string
	mu     sync.Mutex
	text   string
	stop   chan struct{}
	done   chan struct{}
}

func (u *UI) Spinner(text, indent string) *Spinner {
	s := &Spinner{ui: u, indent: indent, text: text}
	if !u.tty {
		return s
	}
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	go s.loop()
	return s
}

func (s *Spinner) loop() {
	defer close(s.done)
	frames := []rune(spinFrames)
	tick := time.NewTicker(80 * time.Millisecond)
	defer tick.Stop()
	for i := 0; ; i++ {
		select {
		case <-s.stop:
			return
		case <-tick.C:
		}
		s.mu.Lock()
		msg := Truncate(s.text, Width()-RuneLen(s.indent)-3)
		s.mu.Unlock()
		s.ui.mu.Lock()
		fmt.Fprintf(s.ui.out, "\r\x1b[K%s%s %s", s.indent, s.ui.Cyan(string(frames[i%len(frames)])), msg)
		s.ui.mu.Unlock()
	}
}

func (s *Spinner) Set(text string) {
	s.mu.Lock()
	s.text = text
	s.mu.Unlock()
}

func (s *Spinner) Stop() {
	if s.stop == nil {
		return
	}
	close(s.stop)
	<-s.done
	s.stop = nil
	s.ui.mu.Lock()
	fmt.Fprint(s.ui.out, "\r\x1b[K")
	s.ui.mu.Unlock()
}
