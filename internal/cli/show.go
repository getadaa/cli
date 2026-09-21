package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

func init() { register(newShowCmd) }

// viewers maps an id prefix to the command that shows that kind of record, so
// any id pasted from anywhere can be looked at without knowing what it is.
var viewers = map[string][]string{
	"per_": {"people", "view"},
	"dev_": {"devices", "view"},
	"fnd_": {"findings", "view"},
	"tsk_": {"tasks", "view"},
	"req_": {"requests", "view"},
	"res_": {"resources", "view"},
	"crd_": {"credentials", "view"},
	"lic_": {"licenses", "view"},
	"sub_": {"subscriptions", "view"},
	"svc_": {"services", "view"},
	"dom_": {"domains", "view"},
	"dns_": {"domains", "dns", "view"},
	"mbx_": {"mail", "mailboxes", "view"},
	"srv_": {"servers", "view"},
	"bkp_": {"backups", "view"},
	"wsp_": {"workspaces", "view"},
	"wac_": {"workspaces", "accounts", "view"},
	"wau_": {"workspaces", "authorization"},
}

func newShowCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show any record by its id",
		Long: `Show any record by its id, whatever kind it is.

Every id starts with what it is — per_ for a person, fnd_ for a finding,
tsk_ for a task — so an id copied from a message, a log or another command's
output is enough.`,
		Example: `  adaa show tsk_01JATX3M4K7Q2YV8N0RCBEZ5HS
  adaa show fnd_01JATX3M4K7Q2YV8N0RCBEZ5HS --json`,
		GroupID: groupCore,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			prefix := ""
			if i := strings.Index(id, "_"); i > 0 {
				prefix = id[:i+1]
			}
			path, ok := viewers[prefix]
			if !ok {
				return usagef("%q is not an id adaa knows how to show", id)
			}
			target, rest, err := cmd.Root().Find(path)
			if err != nil || len(rest) > 0 || target.RunE == nil {
				return usagef("no command shows %s ids yet", prefix)
			}
			target.SetContext(cmd.Context())
			return target.RunE(target, []string{id})
		},
	}
}
