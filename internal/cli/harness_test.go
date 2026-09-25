package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/getadaa/cli/internal/ui"
	"github.com/zalando/go-keyring"
)

// TestMain keeps tests away from the real system keychain.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}

const (
	testOrg        = "org_01JATX3M4K7Q2YV8N0RCBEZ5HS"
	testPerson     = "per_01JATX3M4K7Q2YV8N0RCBEZ5HS"
	testIdentity   = "idn_01JATX3M4K7Q2YV8N0RCBEZ5HS"
	testMembership = "mem_01JATX3M4K7Q2YV8N0RCBEZ5HS"
)

// fakeAPI is an in-process adaa API. Register handlers with on(); every
// request is recorded so tests can assert on what the CLI sent.
type fakeAPI struct {
	t   *testing.T
	srv *httptest.Server
	mux *http.ServeMux

	// identity is what GET /me answers, exposed so a test can give the caller a
	// second membership. Read when the request arrives rather than when the fake
	// is built, so a test may change it after construction.
	identity map[string]any

	mu    sync.Mutex
	calls []call
}

type call struct {
	Method         string
	Path           string
	Query          string
	Body           map[string]any
	IdempotencyKey string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{t: t, mux: http.NewServeMux()}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c := call{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, IdempotencyKey: r.Header.Get("Idempotency-Key")}
		_ = json.Unmarshal(b, &c.Body)
		f.mu.Lock()
		f.calls = append(f.calls, c)
		f.mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(b))
		f.mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	f.identity = map[string]any{
		"identity": map[string]any{
			"id": testIdentity, "full_name": "Kari Nordmann", "email": "kari@firma.no",
		},
		"membership": map[string]any{
			"id": testMembership, "organization_id": testOrg, "role": "org_admin", "person_id": testPerson,
		},
		"person": map[string]any{
			"id": testPerson, "organization_id": testOrg, "full_name": "Kari Nordmann",
			"email": "kari@firma.no", "employment_status": "active",
		},
		"organization": map[string]any{"id": testOrg, "name": "Firma AS", "status": "active"},
		"memberships": []any{map[string]any{
			"membership_id": testMembership, "organization_id": testOrg,
			"organization_name": "Firma AS", "organization_slug": "firma", "role": "org_admin",
		}},
		"permissions": []string{"person.read", "person.write"},
	}
	f.onFunc("GET /me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			writeJSON(w, 401, problem(401, "unauthorized", "Not signed in"))
			return
		}
		writeJSON(w, 200, f.identity)
	})
	return f
}

// on answers pattern (a ServeMux pattern such as "GET /people/{id}") with
// a fixed status and JSON body.
func (f *fakeAPI) on(pattern string, status int, body any) {
	f.onFunc(pattern, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, status, body) })
}

func (f *fakeAPI) onFunc(pattern string, fn http.HandlerFunc) {
	f.mux.HandleFunc(pattern, fn)
}

// page wraps items in the API's list shape.
func page(items ...any) map[string]any {
	if items == nil {
		items = []any{}
	}
	return map[string]any{"items": items, "next_cursor": nil}
}

func problem(status int, code, title string) map[string]any {
	return map[string]any{"type": "https://adaa.no/problems/" + code, "title": title, "status": status}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	ct := "application/json"
	if m, ok := body.(map[string]any); ok && m["type"] != nil && m["title"] != nil && status >= 400 {
		ct = "application/problem+json"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

// requests returns the recorded calls matching method and path.
func (f *fakeAPI) requests(method, path string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.Method == method && c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

type result struct {
	Stdout string
	Stderr string
	Code   int
}

// run executes adaa against the fake API with a token in the environment, no
// terminal, and a throwaway config directory.
func (f *fakeAPI) run(args ...string) result {
	return f.runWithInput("", args...)
}

func (f *fakeAPI) runWithInput(stdin string, args ...string) result {
	f.t.Helper()
	f.t.Setenv("ADAA_CONFIG_DIR", f.t.TempDir())
	f.t.Setenv("ADAA_API_URL", f.srv.URL)
	f.t.Setenv("ADAA_TOKEN", "test-token")
	f.t.Setenv("ADAA_NO_UPDATE_NOTIFIER", "1")
	f.t.Setenv("ADAA_ORG", "")
	f.t.Setenv("NO_COLOR", "1")
	var out, errb bytes.Buffer
	a := &App{IO: ui.Test(strings.NewReader(stdin), &out, &errb)}
	code := a.Run(context.Background(), args)
	return result{Stdout: out.String(), Stderr: errb.String(), Code: code}
}

func (r result) mustSucceed(t *testing.T) result {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	return r
}

func TestStatusRendersHeadline(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/status", 200, map[string]any{
		"organization_id": testOrg, "overall": "attention",
		"headline":     map[string]any{"text": "One backup needs a look."},
		"people":       map[string]any{"active": 6, "planned": 1, "departed_with_access": 0},
		"findings":     map[string]any{"critical": 0, "warning": 1, "info": 0, "open_total": 1},
		"tasks":        map[string]any{"awaiting_approval": 0, "in_progress": 0, "blocked": 0, "open_total": 0},
		"signals":      map[string]any{"ok": 10, "warning": 1, "critical": 0, "unknown": 0},
		"coverage":     map[string]any{"sources_total": 3, "sources_healthy": 3, "sources_stale": 0, "blind_spots": []any{}},
		"cost":         map[string]any{"currency": "NOK", "recurring_monthly_minor": 825000, "lines_awaiting_price": 0},
		"generated_at": "2026-09-21T10:00:00Z",
	})
	r := f.run("status").mustSucceed(t)
	for _, want := range []string{"One backup needs a look.", "6 active", "8 250,00 kr per month"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("status output lacks %q:\n%s", want, r.Stdout)
		}
	}
}

func TestProblemExitCodes(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/status", 403, problem(403, "forbidden", "Ikke tilgang"))
	r := f.run("status")
	if r.Code != exitForbidden {
		t.Fatalf("exit %d, want %d; stderr: %s", r.Code, exitForbidden, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "Ikke tilgang") {
		t.Errorf("stderr lacks the problem title: %s", r.Stderr)
	}
}

func TestNotLoggedIn(t *testing.T) {
	f := newFakeAPI(t)
	f.t.Setenv("ADAA_TOKEN", "")
	t.Setenv("ADAA_CONFIG_DIR", t.TempDir())
	var out, errb bytes.Buffer
	t.Setenv("ADAA_API_URL", f.srv.URL)
	a := &App{IO: ui.Test(strings.NewReader(""), &out, &errb)}
	if code := a.Run(context.Background(), []string{"--no-input", "whoami"}); code != exitAuth {
		t.Fatalf("exit %d, want %d: %s", code, exitAuth, errb.String())
	}
}

func TestAPICommandSubstitutesOrg(t *testing.T) {
	f := newFakeAPI(t)
	f.on("GET /organizations/"+testOrg+"/people", 200, page(map[string]any{"id": testPerson}))
	r := f.run("api", "/organizations/{org}/people").mustSucceed(t)
	if !strings.Contains(r.Stdout, testPerson) {
		t.Fatalf("stdout: %s", r.Stdout)
	}
}

func TestMutationsCarryIdempotencyKey(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /organizations/"+testOrg+"/findings", 201, map[string]any{"id": "fnd_01JATX3M4K7Q2YV8N0RCBEZ5HS"})
	f.run("api", "/organizations/{org}/findings", "-f", "summary=Printer jammed").mustSucceed(t)
	reqs := f.requests("POST", "/organizations/"+testOrg+"/findings")
	if len(reqs) != 1 || reqs[0].IdempotencyKey == "" {
		t.Fatalf("want one request with an Idempotency-Key, got %+v", reqs)
	}
}
