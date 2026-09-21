package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/getadaa/cli/internal/ui"
	"github.com/spf13/cobra"
)

const skillPath = "../../plugins/adaa/skills/adaa/SKILL.md"

// A skill that names a command or flag the binary does not have makes an
// agent confidently wrong, so every `adaa …` in SKILL.md is checked against
// the real command tree.
func TestSkillMatchesCommandTree(t *testing.T) {
	b, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{IO: ui.Test(strings.NewReader(""), &strings.Builder{}, &strings.Builder{})}
	root := a.NewRoot()

	invocations := skillInvocations(string(b))
	if len(invocations) < 20 {
		t.Fatalf("found only %d adaa invocations in SKILL.md; is the parser broken?", len(invocations))
	}
	for _, inv := range invocations {
		if err := checkInvocation(root, inv); err != "" {
			t.Errorf("SKILL.md: `%s`: %s", inv, err)
		}
	}
}

func TestSkillFrontmatter(t *testing.T) {
	b, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.HasPrefix(s, "---\nname: adaa\n") {
		t.Fatal("SKILL.md must start with frontmatter naming the skill adaa")
	}
	if !strings.Contains(s[:strings.Index(s[4:], "---")+4], "description: ") {
		t.Fatal("SKILL.md frontmatter needs a description; it is how agents decide to load the skill")
	}
}

var codeSpan = regexp.MustCompile("`([^`]+)`")

// skillInvocations returns every command line starting with `adaa`, from
// fenced blocks and inline code.
func skillInvocations(doc string) []string {
	var out []string
	fenced := false
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			fenced = !fenced
			continue
		}
		var candidates []string
		if fenced {
			candidates = []string{trimmed}
		} else {
			for _, m := range codeSpan.FindAllStringSubmatch(line, -1) {
				candidates = append(candidates, m[1])
			}
		}
		for _, c := range candidates {
			c = strings.TrimPrefix(strings.TrimSpace(c), "$ ")
			for _, part := range regexp.MustCompile(`\s*(\|\||&&|\||;)\s*`).Split(c, -1) {
				part = strings.TrimSpace(part)
				if strings.HasPrefix(part, "echo ") {
					continue
				}
				if part == "adaa" || strings.HasPrefix(part, "adaa ") {
					out = append(out, part)
				}
			}
		}
	}
	return out
}

func checkInvocation(root *cobra.Command, inv string) string {
	fields := strings.Fields(inv)[1:]
	cmd := root
	descending := true
	for _, f := range fields {
		if strings.HasPrefix(f, "#") || f == ">" || f == "2>&1" {
			break
		}
		if strings.HasPrefix(f, "-") {
			descending = false
			if msg := checkFlag(cmd, f); msg != "" {
				return msg
			}
			continue
		}
		if !descending || strings.HasPrefix(f, "<") {
			descending = false
			continue
		}
		if sub := findSub(cmd, f); sub != nil {
			cmd = sub
			continue
		}
		if cmd == root {
			return "no command " + f
		}
		// A bare word where a subcommand was expected, on a command that only
		// groups others, is a typo rather than an argument.
		if !cmd.Runnable() {
			return "`" + cmd.CommandPath() + "` has no subcommand " + f
		}
		descending = false
	}
	return ""
}

func findSub(cmd *cobra.Command, name string) *cobra.Command {
	for _, c := range cmd.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return c
		}
	}
	return nil
}

func checkFlag(cmd *cobra.Command, f string) string {
	if f == "-" || f == "--" {
		return ""
	}
	if name, ok := strings.CutPrefix(f, "--"); ok {
		name, _, _ = strings.Cut(name, "=")
		if cmd.Flags().Lookup(name) == nil && cmd.InheritedFlags().Lookup(name) == nil && name != "help" {
			return "`" + cmd.CommandPath() + "` has no flag --" + name
		}
		return ""
	}
	short := strings.TrimPrefix(f, "-")
	if len(short) == 1 && cmd.Flags().ShorthandLookup(short) == nil && cmd.InheritedFlags().ShorthandLookup(short) == nil && short != "h" {
		return "`" + cmd.CommandPath() + "` has no flag -" + short
	}
	return ""
}

func TestSkillCheckerCatchesDrift(t *testing.T) {
	a := &App{IO: ui.Test(strings.NewReader(""), &strings.Builder{}, &strings.Builder{})}
	root := a.NewRoot()
	for _, bad := range []string{"adaa peeple list", "adaa people lst", "adaa people list --colour", "adaa findings view fnd_1 --wat"} {
		if checkInvocation(root, bad) == "" {
			t.Errorf("%q passed the drift check", bad)
		}
	}
	for _, good := range []string{"adaa people view kari@firma.no", "adaa status --json", "adaa people add --name x --dry-run -y"} {
		if msg := checkInvocation(root, good); msg != "" {
			t.Errorf("%q failed: %s", good, msg)
		}
	}
}
