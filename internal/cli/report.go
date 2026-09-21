package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/getadaa/cli/internal/diag"
	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() { register(newReportCmd) }

func newReportCmd(a *App) *cobra.Command {
	var detail, severity, about, forPerson, product string
	var blocking, diagnostics bool
	var attach []string
	cmd := &cobra.Command{
		Use:   "report [summary]",
		Short: "Tell adaa something is wrong",
		Long: `Tell adaa something is wrong, in your own words.

You do not need to know what is broken, which system it belongs to or how
serious it is. What matters most is whether it is stopping somebody from
working — say so with --blocking.

--diagnostics attaches a snapshot of this computer (system, disk space,
network, whether name lookups work). You are shown exactly what will be sent
before it goes.`,
		Example: `  adaa report
  adaa report "The printer on the second floor says paper jam but there is no paper stuck"
  adaa report "Cannot reach the file server" --blocking --diagnostics
  adaa report "Laptop fan is loud" --about kari-mbp --attach noise.m4a`,
		GroupID: groupCore,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := oneOf("severity", severity, findingSeverities...); err != nil {
				return err
			}
			summary := strings.TrimSpace(strings.Join(args, " "))
			if err := a.askReport(cmd, &summary, &detail, &blocking); err != nil {
				return err
			}

			body := map[string]any{"summary": summary, "blocking_work": blocking}
			if detail != "" {
				body["detail"] = detail
			}
			if severity != "" {
				body["severity"] = severity
			}
			if product != "" {
				body["product_code"] = product
			}
			if about != "" {
				resourceID, note, err := a.reportSubject(ctx, about)
				if err != nil {
					return err
				}
				if resourceID != "" {
					body["resource_id"] = resourceID
				}
				if note != "" {
					body["detail"] = strings.TrimSpace(note + "\n\n" + detail)
				}
			}
			if forPerson != "" {
				id, err := a.Resolve(ctx, kindPerson, forPerson)
				if err != nil {
					return err
				}
				body["person_id"] = id
			}

			files := append([]string(nil), attach...)
			if diagnostics {
				file, err := a.diagnosticsFile(ctx)
				if err != nil {
					return err
				}
				if file != "" {
					defer os.RemoveAll(filepath.Dir(file))
					files = append(files, file)
				}
			}
			for _, f := range attach {
				if _, err := os.Stat(f); err != nil {
					return usagef("cannot attach %s: %v", f, err)
				}
			}

			path, err := a.OrgPath(ctx, "/findings")
			if err != nil {
				return err
			}
			resp, f, err := a.Send(ctx, http.MethodPost, path, body)
			if err != nil {
				return err
			}
			if len(files) > 0 {
				if _, err := a.attachFiles(ctx, "/findings/"+f.Str("id")+"/attachments", files, ""); err != nil {
					a.IO.Warnf("The problem was reported as %s, but an attachment failed.", f.Str("id"))
					return err
				}
			}
			if a.JSON {
				return a.PrintJSON(resp.Body)
			}
			a.IO.Successf("Reported: %s %s", f.Str("summary"), a.IO.E().Dim(f.Str("id")))
			if dup := f.Str("duplicate_of_finding_id"); dup != "" {
				a.IO.Infof("adaa already knew about this — it is the same problem as %s.", dup)
				a.IO.Hint("adaa findings view %s", dup)
				return nil
			}
			a.IO.Hint("adaa findings view %s", f.Str("id"))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&detail, "detail", "", "More about what happened, what you tried, what you expected")
	f.BoolVar(&blocking, "blocking", false, "Somebody cannot do their job because of this")
	f.StringVar(&severity, "severity", "", "How serious you think it is (a hint; triage may change it)")
	f.StringVar(&about, "about", "", "The device or resource it concerns, by id, name, hostname or serial number")
	f.StringVar(&forPerson, "for", "", "Who is affected, if not you (email, name or id)")
	f.StringVar(&product, "product", "", "The product it concerns, such as mail or backup")
	f.StringArrayVar(&attach, "attach", nil, "Attach a file, such as a screenshot (repeatable)")
	f.BoolVar(&diagnostics, "diagnostics", false, "Attach a snapshot of this computer's system and network")
	enumFlag(cmd, "severity", findingSeverities...)
	_ = cmd.RegisterFlagCompletionFunc("about", a.complete(kindDevice))
	_ = cmd.RegisterFlagCompletionFunc("for", a.complete(kindPerson))
	return cmd
}

// askReport fills in whatever the person has not said yet. Without a terminal
// only the summary is required; the rest have sensible defaults.
func (a *App) askReport(cmd *cobra.Command, summary, detail *string, blocking *bool) error {
	if *summary != "" {
		if a.IO.Interactive() && !a.Yes && !cmd.Flags().Changed("blocking") {
			ok, err := a.IO.Confirm("Is this stopping someone from working?", "--blocking", false)
			if err != nil {
				return err
			}
			*blocking = ok
		}
		return nil
	}
	if !a.IO.Interactive() {
		return &ui.NoInputError{What: "a summary", Flag: "the summary as an argument"}
	}
	fields := []huh.Field{
		huh.NewInput().Title("What is wrong?").Description("In your own words. You do not need to know what is broken.").
			Value(summary).Validate(nonEmpty),
	}
	if !cmd.Flags().Changed("detail") {
		fields = append(fields, huh.NewText().Title("Anything else?").
			Description("What happened, what you tried, what you expected. Optional.").Value(detail))
	}
	if !cmd.Flags().Changed("blocking") {
		fields = append(fields, huh.NewConfirm().Title("Is this stopping someone from working?").
			Affirmative("Yes").Negative("No").Value(blocking))
	}
	return a.IO.Run(huh.NewGroup(fields...))
}

// reportSubject turns --about into a resource id. Devices are what people
// name, but a finding points at a resource, so a device is followed to the
// resource it appears as. A device with no resource yet is named in the
// detail instead, so the information still reaches whoever triages it.
func (a *App) reportSubject(ctx context.Context, about string) (resourceID, note string, err error) {
	if strings.HasPrefix(about, kindResource.Prefix) {
		return about, "", nil
	}
	devID, err := a.Resolve(ctx, kindDevice, about)
	if err != nil {
		return "", "", err
	}
	dev, _, err := a.Get(ctx, "/devices/"+devID, nil)
	if err != nil {
		return "", "", err
	}
	if r := dev.Str("resource_id"); r != "" {
		return r, "", nil
	}
	return "", fmt.Sprintf("About device: %s (%s)", join(", ", dev.Str("name"), dev.Str("serial_number")), devID), nil
}

// diagnosticsFile writes the snapshot to a temporary file, after showing it
// and asking. It returns "" if the person decided not to send it.
func (a *App) diagnosticsFile(ctx context.Context) (string, error) {
	host := a.Cfg.EffectiveAPIURL()
	if u, err := url.Parse(host); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	text, _ := ui.Spin(a.IO, "Collecting diagnostics…", func() (string, error) {
		return diag.Collect(ctx, host), nil
	})
	if a.IO.Interactive() && !a.Yes {
		fmt.Fprintln(a.IO.Err)
		fmt.Fprintln(a.IO.Err, a.IO.E().Dim(text))
		ok, err := a.IO.Confirm("Attach this to the report?", "--yes", true)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", nil
		}
	}
	dir, err := os.MkdirTemp("", "adaa-report-*")
	if err != nil {
		return "", err
	}
	file := filepath.Join(dir, "diagnostics.txt")
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		return "", err
	}
	return file, nil
}
