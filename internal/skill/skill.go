// Package skill installs and checks the adaa agent skill.
//
// There is one SKILL.md, published in the getadaa/plugins marketplace. `adaa
// skill install` downloads that same file, byte for byte, so a skill installed
// from the marketplace and one installed by the CLI cannot drift apart: both
// are compared against the published copy by checksum.
package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	Name = "adaa"
	// Marketplace is the GitHub repository the plugin is published in.
	Marketplace = "getadaa/plugins"
	// PluginID is how Claude Code names the installed plugin.
	PluginID = "adaa@adaa"

	defaultURL = "https://raw.githubusercontent.com/" + Marketplace + "/main/plugins/adaa/skills/adaa/SKILL.md"
	sidecar    = ".adaa-skill.json"
)

// SourceURL is where the published SKILL.md is downloaded from.
func SourceURL() string {
	if u := os.Getenv("ADAA_SKILL_URL"); u != "" {
		return u
	}
	return defaultURL
}

// Checksum identifies a version of the skill.
func Checksum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Short is the first characters of a checksum, for display.
func Short(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}

// Fetch downloads the published skill.
func Fetch(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, SourceURL(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not download the skill from %s: %w", Marketplace, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("could not download the skill from %s: %s", Marketplace, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(string(b), "---\n") || !strings.Contains(string(b), "name: "+Name) {
		return nil, fmt.Errorf("what %s serves at %s is not the adaa skill", Marketplace, SourceURL())
	}
	return b, nil
}

// Scope decides whether an install is personal or belongs to a repository.
type Scope string

const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
)

// Client is one agent that reads skills from a directory of SKILL.md folders.
type Client struct {
	ID   string
	Name string
	// home is the client's config directory; its existence means the client
	// is installed, which is how the picker pre-selects.
	home func() string
	// dir returns the skills directory for a scope, relative to root for project.
	dir func(scope Scope, root string) string
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func envOr(key string, fallback ...string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return filepath.Join(fallback...)
}

// Clients lists every client adaa installs into, in picker order. Only
// locations the clients document are here: a skill written somewhere nothing
// reads is worse than none, because doctor would call it installed.
var Clients = []Client{
	{
		ID: "claude", Name: "Claude Code",
		home: func() string { return envOr("CLAUDE_CONFIG_DIR", homeDir(), ".claude") },
		dir: func(s Scope, root string) string {
			if s == ScopeProject {
				return filepath.Join(root, ".claude", "skills")
			}
			return filepath.Join(envOr("CLAUDE_CONFIG_DIR", homeDir(), ".claude"), "skills")
		},
	},
	{
		ID: "codex", Name: "OpenAI Codex",
		home: func() string { return envOr("CODEX_HOME", homeDir(), ".codex") },
		dir: func(s Scope, root string) string {
			if s == ScopeProject {
				return filepath.Join(root, ".codex", "skills")
			}
			return filepath.Join(envOr("CODEX_HOME", homeDir(), ".codex"), "skills")
		},
	},
	{
		ID: "agents", Name: "Agent Skills (~/.agents, shared by several agents)",
		home: func() string { return filepath.Join(homeDir(), ".agents") },
		dir: func(s Scope, root string) string {
			if s == ScopeProject {
				return filepath.Join(root, ".agents", "skills")
			}
			return filepath.Join(homeDir(), ".agents", "skills")
		},
	},
}

func ClientByID(id string) (Client, bool) {
	for _, c := range Clients {
		if c.ID == strings.ToLower(strings.TrimSpace(id)) {
			return c, true
		}
	}
	return Client{}, false
}

func ClientIDs() []string {
	ids := make([]string, len(Clients))
	for n, c := range Clients {
		ids[n] = c.ID
	}
	return ids
}

// Present reports whether the client seems to be installed on this computer.
func (c Client) Present() bool {
	st, err := os.Stat(c.home())
	return err == nil && st.IsDir()
}

// Path is where this client's copy of the skill lives.
func (c Client) Path(scope Scope, root string) string {
	return filepath.Join(c.dir(scope, root), Name, "SKILL.md")
}

// Origin says who put a copy of the skill where it is.
type Origin string

const (
	OriginCLI         Origin = "adaa skill install"
	OriginMarketplace Origin = "marketplace"
)

// Install is one copy of the skill found on disk.
type Install struct {
	Client string `json:"client"`
	Scope  Scope  `json:"scope,omitempty"`
	Origin Origin `json:"origin"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	// Modified means the file differs from what adaa wrote, so someone edited
	// it; updating would throw their edits away.
	Modified bool `json:"modified"`
}

type record struct {
	SHA256      string    `json:"sha256"`
	Source      string    `json:"source"`
	InstalledAt time.Time `json:"installed_at"`
}

// Write installs content for a client, refusing to overwrite local edits
// unless force is set.
func Write(c Client, scope Scope, root string, content []byte, force bool) (string, error) {
	path := c.Path(scope, root)
	if !force {
		if in, err := inspect(c, scope, path); err == nil && in != nil && in.Modified {
			return path, &EditedError{Path: path}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return path, err
	}
	rec, _ := json.MarshalIndent(record{SHA256: Checksum(content), Source: SourceURL(), InstalledAt: time.Now().UTC()}, "", "  ")
	return path, os.WriteFile(filepath.Join(filepath.Dir(path), sidecar), append(rec, '\n'), 0o644)
}

// EditedError means the installed skill was changed by hand.
type EditedError struct{ Path string }

func (e *EditedError) Error() string {
	return e.Path + " has local edits; pass --force to replace them"
}

// Remove deletes a copy adaa installed. It refuses a directory it did not
// create, so it never deletes a skill someone else put there.
func Remove(c Client, scope Scope, root string) (string, error) {
	path := c.Path(scope, root)
	dir := filepath.Dir(path)
	if _, err := os.Stat(filepath.Join(dir, sidecar)); err != nil {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return path, fs.ErrNotExist
		}
		return path, fmt.Errorf("%s was not installed by adaa; remove it yourself if you want it gone", dir)
	}
	return path, os.RemoveAll(dir)
}

func inspect(c Client, scope Scope, path string) (*Install, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	in := &Install{Client: c.ID, Scope: scope, Origin: OriginCLI, Path: path, SHA256: Checksum(b)}
	var rec record
	if rb, err := os.ReadFile(filepath.Join(filepath.Dir(path), sidecar)); err == nil && json.Unmarshal(rb, &rec) == nil {
		in.Modified = rec.SHA256 != in.SHA256
	}
	return in, nil
}

// Find lists every copy of the skill on this computer: those adaa installed,
// for both scopes, and those installed from the marketplace.
func Find(root string) []Install {
	var out []Install
	seen := map[string]bool{}
	add := func(in *Install) {
		if in == nil {
			return
		}
		abs, err := filepath.Abs(in.Path)
		if err != nil {
			abs = in.Path
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		out = append(out, *in)
	}
	for _, c := range Clients {
		for _, s := range []Scope{ScopeUser, ScopeProject} {
			in, _ := inspect(c, s, c.Path(s, root))
			add(in)
		}
	}
	for _, in := range marketplaceInstalls() {
		add(&in)
	}
	return out
}

// marketplaceInstalls finds the plugin as Claude Code and Codex installed it.
func marketplaceInstalls() []Install {
	var out []Install
	claudeHome := envOr("CLAUDE_CONFIG_DIR", homeDir(), ".claude")
	if b, err := os.ReadFile(filepath.Join(claudeHome, "plugins", "installed_plugins.json")); err == nil {
		var reg struct {
			Plugins map[string][]struct {
				InstallPath string `json:"installPath"`
				Scope       string `json:"scope"`
			} `json:"plugins"`
		}
		if json.Unmarshal(b, &reg) == nil {
			for _, inst := range reg.Plugins[PluginID] {
				path := filepath.Join(inst.InstallPath, "skills", Name, "SKILL.md")
				if sb, err := os.ReadFile(path); err == nil {
					out = append(out, Install{Client: "claude", Scope: Scope(inst.Scope), Origin: OriginMarketplace, Path: path, SHA256: Checksum(sb)})
				}
			}
		}
	}
	codexPlugins := filepath.Join(envOr("CODEX_HOME", homeDir(), ".codex"), "plugins")
	_ = walkDepth(codexPlugins, 6, func(path string) {
		if strings.HasSuffix(filepath.ToSlash(path), "/skills/"+Name+"/SKILL.md") && strings.Contains(filepath.ToSlash(path), "/"+Name+"/") {
			if sb, err := os.ReadFile(path); err == nil {
				out = append(out, Install{Client: "codex", Origin: OriginMarketplace, Path: path, SHA256: Checksum(sb)})
			}
		}
	})
	return out
}

func walkDepth(root string, depth int, fn func(string)) error {
	base := strings.Count(filepath.Clean(root), string(filepath.Separator))
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.Count(path, string(filepath.Separator))-base > depth {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == "SKILL.md" {
			fn(path)
		}
		return nil
	})
}
