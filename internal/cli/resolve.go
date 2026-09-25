package cli

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/getadaa/cli/internal/obj"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

// Kind describes how to find one type of record by something a person would
// type: an email, a hostname, a domain. Nobody remembers a ULID.
type Kind struct {
	Name   string // singular, for messages: "person"
	Plural string // the command group: "people"
	Prefix string // id prefix: "per_"
	// List is the collection path; an org-scoped one starts with "{org}".
	List  string
	Query url.Values
	// Match lists fields compared exactly, ignoring case.
	Match []string
	// Label is how a record appears in a picker and in completion.
	Label func(obj.Obj) string
	// ID is where the identifier lives, for a record that arrives wrapped in
	// something else. Empty means "id".
	ID string
}

// idOf reads the identifier out of one record.
func (k Kind) idOf(o obj.Obj) string {
	if k.ID != "" {
		return o.Str(k.ID)
	}
	return o.Str("id")
}

func (k Kind) listPath(ctx context.Context, a *App) (string, error) {
	if rest, ok := strings.CutPrefix(k.List, "{org}"); ok {
		return a.OrgPath(ctx, rest)
	}
	return k.List, nil
}

var (
	kindPerson = Kind{Name: "person", Plural: "people", Prefix: "per_", List: "{org}/people",
		Match: []string{"email", "full_name"},
		Label: func(o obj.Obj) string { return o.Str("full_name") + " <" + o.Str("email") + ">" }}
	kindDevice = Kind{Name: "device", Plural: "devices", Prefix: "dev_", List: "{org}/devices",
		Match: []string{"name", "hostname", "serial_number", "asset_tag"},
		Label: func(o obj.Obj) string {
			return join(" · ", o.Str("name"), o.Str("model"), o.Str("assigned_person_name"))
		}}
	kindDomain = Kind{Name: "domain", Plural: "domains", Prefix: "dom_", List: "{org}/domains",
		Match: []string{"name"},
		Label: func(o obj.Obj) string { return o.Str("name") }}
	kindServer = Kind{Name: "server", Plural: "servers", Prefix: "srv_", List: "{org}/servers",
		Match: []string{"hostname"},
		Label: func(o obj.Obj) string { return join(" · ", o.Str("hostname"), o.Str("purpose")) }}
	kindMailbox = Kind{Name: "mailbox", Plural: "mail mailboxes", Prefix: "mbx_", List: "{org}/mail/mailboxes",
		Match: []string{"address", "display_name"},
		Label: func(o obj.Obj) string { return join(" · ", o.Str("address"), o.Str("display_name")) }}
	kindCredential = Kind{Name: "credential", Plural: "credentials", Prefix: "crd_", List: "{org}/credentials",
		Match: []string{"name"},
		Label: func(o obj.Obj) string { return join(" · ", o.Str("name"), o.Str("username"), o.Str("target_name")) }}
	kindLicense = Kind{Name: "license", Plural: "licenses", Prefix: "lic_", List: "{org}/licenses",
		Match: []string{"plan_name", "sku"},
		Label: func(o obj.Obj) string { return join(" · ", o.Str("vendor"), o.Str("plan_name")) }}
	kindWorkspace = Kind{Name: "workspace", Plural: "workspaces", Prefix: "wsp_", List: "{org}/workspaces",
		Match: []string{"primary_domain", "display_name"},
		Label: func(o obj.Obj) string { return join(" · ", o.Str("primary_domain"), o.Str("vendor")) }}
	kindSubscription = Kind{Name: "subscription", Plural: "subscriptions", Prefix: "sub_", List: "{org}/subscriptions",
		Match: []string{"service_code", "service_name"},
		Label: func(o obj.Obj) string { return o.Str("service_name") }}
	kindBackup = Kind{Name: "backup job", Plural: "backups", Prefix: "bkp_", List: "{org}/backups",
		Match: []string{"name"},
		Label: func(o obj.Obj) string { return o.Str("name") }}
	kindFinding = Kind{Name: "finding", Plural: "findings", Prefix: "fnd_", List: "{org}/findings",
		Query: url.Values{"status": {"open"}},
		Label: func(o obj.Obj) string { return o.Str("severity") + ": " + o.Str("summary") }}
	kindTask = Kind{Name: "task", Plural: "tasks", Prefix: "tsk_", List: "{org}/tasks",
		Label: func(o obj.Obj) string { return strings.ReplaceAll(o.Str("status"), "_", " ") + ": " + o.Str("summary") }}
	kindRequest = Kind{Name: "request", Plural: "requests", Prefix: "req_", List: "{org}/requests",
		Match: []string{"title"},
		Label: func(o obj.Obj) string { return o.Str("title") }}
	kindResource = Kind{Name: "resource", Plural: "resources", Prefix: "res_", List: "{org}/resources",
		Match: []string{"display_name", "external_id"},
		Label: func(o obj.Obj) string { return join(" · ", o.Str("display_name"), o.Str("kind")) }}
	// A membership is who may act on the company, which is a different list
	// from who works there: most employees have no membership, and somebody
	// with a membership need not be an employee.
	kindMembership = Kind{Name: "member", Plural: "members", Prefix: "mem_", List: "{org}/members",
		ID:    "membership.id",
		Match: []string{"identity.email", "identity.full_name"},
		Label: func(o obj.Obj) string {
			label := o.Str("identity.full_name") + " <" + o.Str("identity.email") + ">"
			if o.Str("membership.revoked_at") != "" {
				return label + " (revoked)"
			}
			return label + " · " + peopleRoleLabel(o.Str("membership.role"))
		}}
	kindToken = Kind{Name: "token", Plural: "tokens", Prefix: "tok_", List: "/tokens",
		Match: []string{"name", "prefix"},
		Label: func(o obj.Obj) string { return o.Str("name") }}
	kindService = Kind{Name: "service", Plural: "services", Prefix: "svc_", List: "/services",
		Match: []string{"code", "name"},
		Label: func(o obj.Obj) string { return o.Str("name") + " (" + o.Str("code") + ")" }}
)

func join(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// Resolve turns what the person typed into an id.
//
// An id passes straight through. Anything else is matched against the list:
// exactly on the Match fields first, then as a substring of the label. One
// hit wins; several are offered in a picker, or listed in the error when
// there is no terminal. Empty input opens the picker over everything.
func (a *App) Resolve(ctx context.Context, k Kind, input string) (string, error) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, k.Prefix) && len(input) == len(k.Prefix)+26 {
		return input, nil
	}
	if k.Prefix == kindPerson.Prefix && strings.EqualFold(input, "me") {
		me, err := a.Identity(ctx)
		if err != nil {
			return "", err
		}
		return me.Str("person.id"), nil
	}
	items, err := a.kindItems(ctx, k)
	if err != nil {
		return "", err
	}
	if input == "" {
		if len(items) == 0 {
			return "", fmt.Errorf("there are no %s yet", pluralOf(k))
		}
		return a.pick(k, items, "Which "+k.Name+"?")
	}

	var exact, partial []obj.Obj
	needle := strings.ToLower(input)
	for _, it := range items {
		matched := false
		for _, f := range k.Match {
			if strings.EqualFold(it.Str(f), input) {
				exact = append(exact, it)
				matched = true
				break
			}
		}
		if !matched && strings.Contains(strings.ToLower(k.Label(it)), needle) {
			partial = append(partial, it)
		}
	}
	candidates := exact
	if len(candidates) == 0 {
		candidates = partial
	}
	switch len(candidates) {
	case 0:
		return "", &notFoundError{kind: k, input: input}
	case 1:
		return k.idOf(candidates[0]), nil
	}
	if a.IO.Interactive() {
		return a.pick(k, candidates, fmt.Sprintf("Several %s match %q. Which one?", pluralOf(k), input))
	}
	var lines []string
	for _, c := range candidates[:min(len(candidates), 10)] {
		lines = append(lines, "  "+k.idOf(c)+"  "+k.Label(c))
	}
	return "", usagef("%q matches %d %s; use the id:\n%s", input, len(candidates), pluralOf(k), strings.Join(lines, "\n"))
}

func (a *App) kindItems(ctx context.Context, k Kind) ([]obj.Obj, error) {
	path, err := k.listPath(ctx, a)
	if err != nil {
		return nil, err
	}
	l, err := ui.Spin(a.IO, "Looking up "+pluralOf(k)+"…", func() (*Listing, error) {
		return a.List(ctx, path, k.Query, ListOpts{Limit: 1000})
	})
	if err != nil {
		return nil, err
	}
	return l.Items, nil
}

func (a *App) pick(k Kind, items []obj.Obj, title string) (string, error) {
	opts := make([]ui.Option, len(items))
	for n, it := range items {
		opts[n] = ui.Option{Label: k.Label(it), Value: k.idOf(it)}
	}
	return a.IO.Select(title, "the "+k.Name+" as an argument", opts)
}

func pluralOf(k Kind) string {
	if i := strings.LastIndex(k.Plural, " "); i >= 0 {
		return k.Plural[i+1:]
	}
	return k.Plural
}

type notFoundError struct {
	kind  Kind
	input string
}

func (e *notFoundError) Error() string {
	return fmt.Sprintf("no %s matches %q (see `adaa %s list`)", e.kind.Name, e.input, e.kind.Plural)
}

// complete offers ids with their labels to the shell, so `adaa people view
// <TAB>` lists names.
func (a *App) complete(k Kind) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		path, err := k.listPath(cmd.Context(), a)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveError
		}
		l, err := a.List(cmd.Context(), path, k.Query, ListOpts{Limit: 200})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveError
		}
		var out []cobra.Completion
		for _, it := range l.Items {
			out = append(out, cobra.CompletionWithDesc(k.idOf(it), k.Label(it)))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// arg returns the first positional argument, or "" so Resolve opens a picker.
func arg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}
