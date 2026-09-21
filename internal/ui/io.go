// Package ui is everything a person sees: tables, detail views, prompts,
// spinners and the words used for money and time.
//
// Data goes to Out. Everything else — progress, prompts, confirmations, hints —
// goes to Err, so `adaa people list | wc -l` counts people and nothing else.
package ui

import (
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer

	InTTY  bool
	OutTTY bool
	ErrTTY bool

	// NoInput forbids prompting even when a terminal is attached.
	NoInput bool
	Color   bool

	out *lipgloss.Renderer
	err *lipgloss.Renderer
}

// System wires up the real terminal.
func System() *IO {
	io := &IO{
		In:     os.Stdin,
		Out:    os.Stdout,
		Err:    os.Stderr,
		InTTY:  isTerminal(os.Stdin),
		OutTTY: isTerminal(os.Stdout),
		ErrTTY: isTerminal(os.Stderr),
	}
	io.Color = colorWanted() && io.OutTTY
	io.NoInput = os.Getenv("ADAA_NO_INPUT") != "" || os.Getenv("CI") != ""
	io.init()
	return io
}

// Test builds an IO over buffers, with no terminal and no color.
func Test(in io.Reader, out, err io.Writer) *IO {
	io := &IO{In: in, Out: out, Err: err}
	io.init()
	return io
}

func (i *IO) init() {
	i.out = lipgloss.NewRenderer(i.Out)
	i.err = lipgloss.NewRenderer(i.Err)
	if !i.Color {
		i.out.SetColorProfile(termenv.Ascii)
		i.err.SetColorProfile(termenv.Ascii)
	}
}

// DisableColor is for --no-color, which is parsed after IO is built.
func (i *IO) DisableColor() {
	i.Color = false
	i.init()
}

// Interactive reports whether a person can answer a prompt right now.
func (i *IO) Interactive() bool {
	return i.InTTY && i.ErrTTY && !i.NoInput
}

// Width is the terminal width, or 0 when output is not a terminal, meaning
// "do not truncate".
func (i *IO) Width() int {
	f, ok := i.Out.(*os.File)
	if !ok || !i.OutTTY {
		return 0
	}
	w, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}
	return w
}

func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// colorWanted follows https://no-color.org and the dumb-terminal convention.
func colorWanted() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return true
}
