package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/getadaa/cli/internal/api"
	"github.com/spf13/cobra"
)

func init() { register(newAPICmd) }

func newAPICmd(a *App) *cobra.Command {
	var method, input string
	var rawFields, typedFields, query []string
	var paginate bool
	cmd := &cobra.Command{
		Use:   "api <path>",
		Short: "Call any API endpoint directly",
		Long: `Call the adaa Admin API directly, with your credentials.

This reaches everything the API offers, including what has no command yet.
{org} in the path is replaced with your organization's id.

  -f key=value   adds a string field to the JSON body
  -F key=value   adds a typed field: true, false, null, numbers, and @file
                 (reads the file) are converted; anything else is a string
  -q key=value   adds a query parameter

A body makes the default method POST; otherwise it is GET. Mutations get an
Idempotency-Key automatically. The response body is printed as-is.

API reference: https://adaa.no/api/openapi.yaml`,
		Example: `  adaa api /me
  adaa api '/organizations/{org}/people' -q employment_status=active --paginate
  adaa api -X PATCH /people/per_01J… -f job_title="Head of Sales"
  adaa api '/organizations/{org}/findings' -f summary="Printer is jammed" -F blocking_work=true`,
		GroupID: groupSetup,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			path := args[0]
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			// Paths copied from older docs carry the version the base URL already has.
			if rest, ok := strings.CutPrefix(path, "/v1/"); ok && strings.HasSuffix(a.Cfg.EffectiveAPIURL(), "/v1") {
				path = "/" + rest
			}
			if strings.Contains(path, "{org}") || strings.Contains(path, "{organization_id}") {
				org, err := a.OrgID(ctx)
				if err != nil {
					return err
				}
				path = strings.NewReplacer("{org}", org, "{organization_id}", org).Replace(path)
			}

			var body any
			if input != "" {
				var r io.Reader = a.IO.In
				if input != "-" {
					f, err := os.Open(input)
					if err != nil {
						return err
					}
					defer f.Close()
					r = f
				}
				b, err := io.ReadAll(r)
				if err != nil {
					return err
				}
				body = b
			} else if len(rawFields)+len(typedFields) > 0 {
				m := map[string]any{}
				for _, kv := range rawFields {
					k, v, ok := strings.Cut(kv, "=")
					if !ok {
						return usagef("-f wants key=value, got %q", kv)
					}
					m[k] = v
				}
				for _, kv := range typedFields {
					k, v, ok := strings.Cut(kv, "=")
					if !ok {
						return usagef("-F wants key=value, got %q", kv)
					}
					tv, err := typedValue(v)
					if err != nil {
						return err
					}
					m[k] = tv
				}
				body = m
			}

			if method == "" {
				method = http.MethodGet
				if body != nil {
					method = http.MethodPost
				}
			}
			method = strings.ToUpper(method)

			q := url.Values{}
			for _, kv := range query {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return usagef("-q wants key=value, got %q", kv)
				}
				q.Add(k, v)
			}

			c, err := a.Client()
			if err != nil {
				return err
			}
			if paginate && method == http.MethodGet {
				items, _, err := c.List(ctx, path, q, 0)
				if err != nil {
					return err
				}
				if items == nil {
					items = []json.RawMessage{}
				}
				b, _ := json.Marshal(map[string]any{"items": items, "next_cursor": nil})
				return a.PrintJSON(b)
			}
			resp, err := c.Do(ctx, api.Request{Method: method, Path: path, Query: q, Body: body})
			if err != nil {
				var p *api.Problem
				if isProblem(err, &p) && len(p.Raw) > 0 {
					_ = a.PrintJSON(p.Raw)
					return errSilentWith(err)
				}
				return err
			}
			if len(resp.Body) == 0 {
				if a.IO.ErrTTY {
					a.IO.Infof("%s", a.dim(fmt.Sprintf("%d %s", resp.Status, http.StatusText(resp.Status))))
				}
				return nil
			}
			return a.PrintJSON(resp.Body)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&method, "method", "X", "", "HTTP method (default GET, or POST with a body)")
	f.StringArrayVarP(&rawFields, "field", "f", nil, "String field for the JSON body, as key=value")
	f.StringArrayVarP(&typedFields, "typed-field", "F", nil, "Typed field for the JSON body, as key=value")
	f.StringArrayVarP(&query, "query", "q", nil, "Query parameter, as key=value")
	f.StringVar(&input, "input", "", "Read the request body from a file, or - for stdin")
	f.BoolVar(&paginate, "paginate", false, "Follow next_cursor and return every item")
	enumFlag(cmd, "method", "GET", "POST", "PUT", "PATCH", "DELETE")
	return cmd
}

func typedValue(v string) (any, error) {
	switch v {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n, nil
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		return n, nil
	}
	if file, ok := strings.CutPrefix(v, "@"); ok {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var j any
		if json.Unmarshal(b, &j) == nil {
			return j, nil
		}
		return string(b), nil
	}
	return v, nil
}
