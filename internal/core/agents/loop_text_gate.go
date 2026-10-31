package agents

import (
	"bytes"
	"errors"
	"unicode/utf8"
)

// TextObserver receives provisional decoded text, never provider metadata or
// arguments. An error closes presentation, not the independent billing stream.
type TextObserver func(string) error

var errTextCapture = errors.New("text capture refused")

// pinnedTextGate exists only inside Secret.WithBytes. Holding the possible
// prefix prevents a literal match split over any number of decoded callbacks
// from reaching a view before its last byte makes the match detectable.
type pinnedTextGate struct {
	value   []byte
	pending []byte
	observe TextObserver
	failed  bool
}

func (g *pinnedTextGate) text(text string) error {
	if g.failed {
		return errTextCapture
	}
	g.pending = append(g.pending, text...)
	if len(g.value) != 0 && bytes.Contains(g.pending, g.value) {
		g.failed = true
		clear(g.pending)
		g.pending = nil
		return errTextCapture
	}
	keep := min(len(g.pending), len(g.value)-1)
	for keep > 0 && !bytes.Equal(g.pending[len(g.pending)-keep:], g.value[:keep]) {
		keep--
	}
	cut := len(g.pending) - max(keep, 0)
	for cut > 0 && cut < len(g.pending) && !utf8.RuneStart(g.pending[cut]) {
		cut--
	}
	return g.emit(cut)
}

func (g *pinnedTextGate) emit(n int) error {
	if n == 0 {
		return nil
	}
	if g.observe != nil {
		if err := g.observe(string(g.pending[:n])); err != nil {
			g.failed = true
			clear(g.pending)
			g.pending = nil
			return err
		}
	}
	remaining := copy(g.pending, g.pending[n:])
	clear(g.pending[remaining:])
	g.pending = g.pending[:remaining]
	return nil
}

func (g *pinnedTextGate) finish() error {
	if g.failed {
		return errTextCapture
	}
	return g.emit(len(g.pending))
}

func (g *pinnedTextGate) close() {
	clear(g.pending)
	g.pending = nil
	g.value = nil
}
