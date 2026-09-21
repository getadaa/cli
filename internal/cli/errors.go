package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/getadaa/cli/internal/api"
	"github.com/getadaa/cli/internal/config"
	"github.com/getadaa/cli/internal/ui"
)

// Exit codes are part of the interface: scripts and agents branch on them.
const (
	exitError       = 1   // anything not listed below
	exitUsage       = 2   // bad flags or arguments, or input needed and no terminal
	exitAuth        = 3   // not logged in, or the credential stopped working
	exitForbidden   = 4   // logged in, but not allowed
	exitNotFound    = 5   // the thing does not exist
	exitRefused     = 6   // the API understood and said no: conflict, invalid, blocked
	exitUnavailable = 7   // the API could not be reached or failed on its side
	exitCancelled   = 130 // the person backed out
)

var (
	errNotLoggedIn = errors.New("not logged in")
	// errSilent means the command already explained itself, as doctor does.
	errSilent = errors.New("")
)

type usageError struct{ error }

func (e *usageError) Unwrap() error { return e.error }

func usagef(format string, a ...any) error {
	return &usageError{fmt.Errorf(format, a...)}
}

func isUsageError(err error) bool {
	var u *usageError
	if errors.As(err, &u) {
		return true
	}
	// Cobra's own argument validation errors are plain errors with these shapes.
	msg := err.Error()
	for _, p := range []string{"unknown command", "unknown flag", "unknown shorthand", "accepts ", "requires at least", "requires at most", "invalid argument", "flag needs an argument", "required flag"} {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

func exitCode(err error) int {
	var noInput *ui.NoInputError
	var problem *api.Problem
	var network *api.NetworkError
	var corrupt *config.CorruptError
	var notFound *notFoundError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ui.ErrCancelled), errors.Is(err, context.Canceled):
		return exitCancelled
	case errors.As(err, &noInput), isUsageError(err):
		return exitUsage
	case errors.Is(err, errNotLoggedIn):
		return exitAuth
	case errors.As(err, &network):
		return exitUnavailable
	case errors.As(err, &corrupt):
		return exitError
	case errors.As(err, &notFound):
		return exitNotFound
	case errors.As(err, &problem):
		switch {
		case problem.Status == 401:
			return exitAuth
		case problem.Status == 403:
			return exitForbidden
		case problem.Status == 404, problem.Status == 410:
			return exitNotFound
		case problem.Status >= 500:
			return exitUnavailable
		case problem.Status >= 400:
			return exitRefused
		}
	}
	return exitError
}

// printError explains an error in a way that says what to do next.
func (a *App) printError(err error) {
	io := a.IO
	e := io.E()

	if errors.Is(err, ui.ErrCancelled) || errors.Is(err, context.Canceled) {
		io.Infof("Cancelled.")
		return
	}

	var problem *api.Problem
	if a.JSON && errors.As(err, &problem) && len(problem.Raw) > 0 {
		var compact bytes.Buffer
		if json.Compact(&compact, problem.Raw) == nil {
			fmt.Fprintln(io.Err, compact.String())
			return
		}
	}

	io.Failf("%s", err.Error())

	var network *api.NetworkError
	switch {
	case errors.Is(err, errNotLoggedIn):
		io.Hint("adaa login")
	case errors.As(err, &network):
		io.Hint("check your connection, then run `adaa doctor`")
	case errors.As(err, &problem):
		if problem.HowToResolve != "" {
			fmt.Fprintln(io.Err, "  "+problem.HowToResolve)
		}
		for _, fe := range problem.Errors {
			msg := fe.Message
			if msg == "" {
				msg = fe.Detail
			}
			fmt.Fprintf(io.Err, "  %s %s\n", e.Bold(fe.Name()+":"), msg)
		}
		if problem.PossibleFrom != nil && *problem.PossibleFrom != "" {
			fmt.Fprintf(io.Err, "  Possible from %s.\n", *problem.PossibleFrom)
		}
		switch problem.ResolvableBy {
		case "adaa":
			fmt.Fprintln(io.Err, e.Dim("  This is on adaa's side. Retrying will not help; adaa has to act."))
		case "losing_registrar":
			fmt.Fprintln(io.Err, e.Dim("  This is waiting on the current registrar, not on you or adaa."))
		}
		if problem.Status == 401 {
			io.Hint("adaa login")
		}
		if a.Debug && problem.Type != "" {
			fmt.Fprintln(io.Err, e.Dim("  type: "+problem.Type))
		}
	}
}

func isProblem(err error, p **api.Problem) bool { return errors.As(err, p) }

// silentWith keeps an error's exit code but prints nothing more, for commands
// that have already written the error body themselves.
type silentWith struct{ err error }

func (s *silentWith) Error() string { return "" }
func (s *silentWith) Unwrap() error { return s.err }

func errSilentWith(err error) error { return &silentWith{err} }
