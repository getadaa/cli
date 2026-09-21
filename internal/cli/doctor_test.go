package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getadaa/cli/internal/skill"
)

const publishedSkill = "---\nname: adaa\ndescription: test\n---\n\n# adaa v2\n"

// skillWorld points every location doctor and skill look at into a temp dir,
// and serves the published skill and the latest release from a fake server.
type skillWorld struct {
	home, claude, codex string
}

func newSkillWorld(t *testing.T, f *fakeAPI) skillWorld {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/SKILL.md"):
			_, _ = w.Write([]byte(publishedSkill))
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			writeJSON(w, 200, map[string]any{"tag_name": "v9.9.9"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	home := t.TempDir()
	w := skillWorld{home: home, claude: filepath.Join(home, ".claude"), codex: filepath.Join(home, ".codex")}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", w.claude)
	t.Setenv("CODEX_HOME", w.codex)
	t.Setenv("ADAA_SKILL_URL", srv.URL+"/plugins/adaa/skills/adaa/SKILL.md")
	t.Setenv("ADAA_GITHUB_API", srv.URL)
	return w
}

func (w skillWorld) claudeSkill() string {
	return filepath.Join(w.claude, "skills", "adaa", "SKILL.md")
}

func TestSkillInstallWritesPublishedFile(t *testing.T) {
	f := newFakeAPI(t)
	w := newSkillWorld(t, f)
	f.run("skill", "install", "--client", "claude").mustSucceed(t)
	b, err := os.ReadFile(w.claudeSkill())
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != publishedSkill {
		t.Fatalf("installed skill differs from the published one:\n%s", b)
	}
}

func TestDoctorFixUpdatesOutdatedSkill(t *testing.T) {
	f := newFakeAPI(t)
	w := newSkillWorld(t, f)
	f.run("skill", "install", "--client", "claude").mustSucceed(t)

	// Simulate an older published version having been installed.
	old := "---\nname: adaa\n---\n\n# adaa v1\n"
	if err := os.WriteFile(w.claudeSkill(), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(filepath.Dir(w.claudeSkill()), ".adaa-skill.json")
	b, _ := json.Marshal(map[string]string{"sha256": checksumOf(old)})
	if err := os.WriteFile(rec, b, 0o644); err != nil {
		t.Fatal(err)
	}

	r := f.run("doctor")
	if !strings.Contains(r.Stdout, "Skill for Claude Code is outdated") {
		t.Fatalf("doctor did not notice the outdated skill:\n%s", r.Stdout)
	}
	f.run("doctor", "--fix")
	got, _ := os.ReadFile(w.claudeSkill())
	if string(got) != publishedSkill {
		t.Fatalf("doctor --fix did not update the skill:\n%s", got)
	}
}

func TestDoctorLeavesEditedSkillAlone(t *testing.T) {
	f := newFakeAPI(t)
	w := newSkillWorld(t, f)
	f.run("skill", "install", "--client", "claude").mustSucceed(t)
	edited := publishedSkill + "\nMy own notes.\n"
	if err := os.WriteFile(w.claudeSkill(), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	r := f.run("doctor", "--fix")
	if !strings.Contains(r.Stdout, "edited by hand") {
		t.Fatalf("doctor did not report the edit:\n%s", r.Stdout)
	}
	got, _ := os.ReadFile(w.claudeSkill())
	if string(got) != edited {
		t.Fatal("doctor --fix overwrote a hand-edited skill")
	}
}

func TestDoctorFlagsDuplicateClaudeInstall(t *testing.T) {
	f := newFakeAPI(t)
	w := newSkillWorld(t, f)
	f.run("skill", "install", "--client", "claude").mustSucceed(t)

	pluginDir := filepath.Join(w.claude, "plugins", "cache", "adaa", "adaa", "1.0.0")
	if err := os.MkdirAll(filepath.Join(pluginDir, "skills", "adaa"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(pluginDir, "skills", "adaa", "SKILL.md"), []byte(publishedSkill), 0o644)
	reg, _ := json.Marshal(map[string]any{"version": 2, "plugins": map[string]any{
		"adaa@adaa": []any{map[string]any{"scope": "user", "installPath": pluginDir}},
	}})
	_ = os.WriteFile(filepath.Join(w.claude, "plugins", "installed_plugins.json"), reg, 0o644)

	r := f.run("doctor", "--json")
	var out struct {
		Checks []check `json:"checks"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, r.Stdout)
	}
	found := false
	for _, c := range out.Checks {
		if c.ID == "skill-duplicate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("doctor did not flag the duplicate install:\n%s", r.Stdout)
	}
}

func TestDoctorReportsExpiredSession(t *testing.T) {
	f := newFakeAPI(t)
	newSkillWorld(t, f)
	f.mux = http.NewServeMux()
	f.on("GET /me", 401, problem(401, "unauthorized", "Ikke innlogget"))
	r := f.run("doctor")
	if r.Code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", r.Code, r.Stdout)
	}
	if !strings.Contains(r.Stdout, "ADAA_TOKEN is not valid") {
		t.Fatalf("doctor did not explain the bad token:\n%s", r.Stdout)
	}
}

func checksumOf(s string) string {
	return skill.Checksum([]byte(s))
}
