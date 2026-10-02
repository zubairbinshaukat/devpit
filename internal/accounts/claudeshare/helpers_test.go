package claudeshare

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every test works in folders it creates under t.TempDir(). Nothing here
// ever reads or writes the real ~/.claude, ~/.claude.json or ~/.devpit.

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func put(t *testing.T, path, content string) {
	t.Helper()
	must(t, os.MkdirAll(fsPath(filepath.Dir(path)), 0o700))
	must(t, os.WriteFile(fsPath(path), []byte(content), 0o600))
}

type fixture struct {
	base, home, def, defJSON, target string
	r                                Roots
}

// newFixture makes an empty default home and an account folder that looks
// like a Claude Code folder. Links are allowed by the probe unless a test
// says otherwise; Windows tests use the real check.
func newFixture(t *testing.T) fixture {
	t.Helper()
	base := t.TempDir()
	f := fixture{base: base}
	f.home = filepath.Join(base, "home")
	f.def = filepath.Join(f.home, ".claude")
	f.defJSON = filepath.Join(f.home, ".claude.json")
	f.target = filepath.Join(base, "accounts", "work")
	must(t, os.MkdirAll(f.def, 0o700))
	put(t, filepath.Join(f.target, ".claude.json"), "{\n  \"numStartups\": 3\n}\n")
	f.r = Roots{
		DefaultHome: f.def, DefaultClaudeJSON: f.defJSON, Target: f.target, TargetName: "work",
		Now:   func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) },
		probe: func(string, string) (bool, string) { return true, "" },
	}
	return f
}

func (f fixture) src(rel ...string) string { return filepath.Join(append([]string{f.def}, rel...)...) }

func (f fixture) dst(rel ...string) string {
	return filepath.Join(append([]string{f.target}, rel...)...)
}

// rich fills the fixture with one of everything, conflicts included.
func (f fixture) rich(t *testing.T) {
	t.Helper()
	put(t, f.src("skills", "alpha", "SKILL.md"), "---\nname: alpha\n---\nalpha\n")
	put(t, f.src("skills", "same", "SKILL.md"), "same\n")
	put(t, f.dst("skills", "same", "SKILL.md"), "same\n")
	put(t, f.src("skills", "diff", "SKILL.md"), "from default\n")
	put(t, f.dst("skills", "diff", "SKILL.md"), "from work\n")
	put(t, f.dst("skills", "mine", "SKILL.md"), "made in work\n")
	put(t, f.src("skills", "synced", "x.md"), "per account\n")
	put(t, f.dst("skills", "synced", "y.md"), "per account too\n")
	put(t, f.src("agents", "a.md"), "agent a\n")
	put(t, f.src("agents", "c.md"), "agent c default\n")
	put(t, f.dst("agents", "b.md"), "agent b\n")
	put(t, f.dst("agents", "c.md"), "agent c work\n")
	put(t, f.src("commands", "go.md"), "command\n")
	put(t, f.src("CLAUDE.md"), "Default rules.\n")
	put(t, f.dst("CLAUDE.md"), "Work rules.\n")
	put(t, f.src("settings.json"), `{
  "model": "opus",
  "env": {
    "SOME_FLAG": "1"
  },
  "enabledPlugins": {
    "fmt@tools": true,
    "lint@tools": true
  },
  "extraKnownMarketplaces": {
    "tools": {
      "source": {
        "source": "github",
        "repo": "example/tools"
      }
    }
  },
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "echo done"
          }
        ]
      }
    ]
  }
}
`)
	put(t, f.dst("settings.json"), `{
  "model": "sonnet",
  "enabledPlugins": {
    "lint@tools": false
  }
}
`)
	put(t, f.defJSON, `{
  "numStartups": 40,
  "userID": "default-user",
  "mcpServers": {
    "github": {
      "type": "stdio",
      "command": "gh-mcp",
      "env": {
        "GITHUB_PAT": "not-a-real-value"
      }
    },
    "files": {
      "type": "stdio",
      "command": "files-mcp"
    }
  }
}
`)
	put(t, f.src("projects", "C--work-app", "s1.jsonl"), "{\"a\":1}\n")
	put(t, f.src("history.jsonl"), "{\"display\":\"hi\"}\n")
}

type stepView struct {
	op       Op
	from, to string
}

// view shows a step with paths relative to the fixture, as S\... (default)
// and T\... (account).
func (f fixture) view(s Step) stepView {
	rel := func(p string) string {
		if p == "" {
			return ""
		}
		if r, ok := relInside(f.def, p); ok {
			return filepath.ToSlash(filepath.Join("S", r))
		}
		if r, ok := relInside(f.target, p); ok {
			return filepath.ToSlash(filepath.Join("T", r))
		}
		return p
	}
	return stepView{op: s.Op, from: rel(s.From), to: rel(s.To)}
}

func (f fixture) views(p Plan) []stepView {
	var out []stepView
	for _, s := range p.Steps {
		out = append(out, f.view(s))
	}
	return out
}

func hasStep(vs []stepView, want stepView) bool {
	for _, v := range vs {
		if v == want {
			return true
		}
	}
	return false
}

func dump(vs []stepView) string {
	var b strings.Builder
	for _, v := range vs {
		b.WriteString(string(v.op) + " " + v.from + " -> " + v.to + "\n")
	}
	return b.String()
}
