package ui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
)

// NoInputError is returned instead of prompting when nobody can answer. It
// names the flag that would have avoided the prompt, because "not a terminal"
// on its own tells an agent nothing about what to do next.
type NoInputError struct {
	What string
	Flag string
}

func (e *NoInputError) Error() string {
	if e.Flag == "" {
		return fmt.Sprintf("%s is needed, and there is no terminal to ask in", e.What)
	}
	return fmt.Sprintf("%s is needed, and there is no terminal to ask in: pass %s", e.What, e.Flag)
}

// ErrCancelled means the person backed out of a prompt.
var ErrCancelled = errors.New("cancelled")

func (i *IO) form(groups ...*huh.Group) *huh.Form {
	return huh.NewForm(groups...).
		WithInput(i.In).
		WithOutput(i.Err).
		WithTheme(huh.ThemeBase16()).
		WithAccessible(os.Getenv("ACCESSIBLE") != "")
}

// Run runs a form built by the caller, for multi-field flows like onboarding.
func (i *IO) Run(groups ...*huh.Group) error {
	if !i.Interactive() {
		return &NoInputError{What: "input"}
	}
	return mapErr(i.form(groups...).Run())
}

func mapErr(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrCancelled
	}
	return err
}

// Confirm asks a yes/no question. flag is what a non-interactive caller should
// pass instead, usually "--yes".
func (i *IO) Confirm(question, flag string, def bool) (bool, error) {
	if !i.Interactive() {
		return false, &NoInputError{What: "confirmation", Flag: flag}
	}
	ok := def
	err := i.form(huh.NewGroup(huh.NewConfirm().Title(question).Affirmative("Yes").Negative("No").Value(&ok))).Run()
	return ok, mapErr(err)
}

// Input asks for one line of text.
func (i *IO) Input(title, flag string, value *string, validate func(string) error) error {
	if !i.Interactive() {
		return &NoInputError{What: strings.ToLower(title), Flag: flag}
	}
	in := huh.NewInput().Title(title).Value(value)
	if validate != nil {
		in = in.Validate(validate)
	}
	return mapErr(i.form(huh.NewGroup(in)).Run())
}

// Secret asks for text without echoing it.
func (i *IO) Secret(title, flag string, value *string) error {
	if !i.Interactive() {
		return &NoInputError{What: strings.ToLower(title), Flag: flag}
	}
	in := huh.NewInput().Title(title).EchoMode(huh.EchoModePassword).Value(value)
	return mapErr(i.form(huh.NewGroup(in)).Run())
}

// Option is one choice in Select or MultiSelect.
type Option struct {
	Label string
	Value string
}

// Select asks for one choice, with type-to-filter once the list is long.
func (i *IO) Select(title, flag string, opts []Option) (string, error) {
	if !i.Interactive() {
		return "", &NoInputError{What: strings.ToLower(title), Flag: flag}
	}
	if len(opts) == 0 {
		return "", errors.New("nothing to choose from")
	}
	var v string
	hopts := make([]huh.Option[string], len(opts))
	for n, o := range opts {
		hopts[n] = huh.NewOption(o.Label, o.Value)
	}
	sel := huh.NewSelect[string]().Title(title).Options(hopts...).Value(&v)
	if len(opts) > 8 {
		sel = sel.Height(12).Filtering(true)
	}
	return v, mapErr(i.form(huh.NewGroup(sel)).Run())
}

// MultiSelect asks for any number of choices; selected are pre-ticked.
func (i *IO) MultiSelect(title, flag string, opts []Option, selected []string) ([]string, error) {
	if !i.Interactive() {
		return nil, &NoInputError{What: strings.ToLower(title), Flag: flag}
	}
	v := append([]string(nil), selected...)
	hopts := make([]huh.Option[string], len(opts))
	for n, o := range opts {
		hopts[n] = huh.NewOption(o.Label, o.Value)
	}
	ms := huh.NewMultiSelect[string]().Title(title).Options(hopts...).Value(&v)
	if len(opts) > 8 {
		ms = ms.Height(14).Filterable(true)
	}
	return v, mapErr(i.form(huh.NewGroup(ms)).Run())
}
