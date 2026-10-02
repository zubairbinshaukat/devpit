package service

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/shims"
)

// troubleshootingDir is the docs site's troubleshooting pages, from this
// package's folder.
var troubleshootingDir = filepath.Join("..", "..", "..", "web", "docs-site", "src", "content", "docs", "troubleshooting")

// problemKinds reads every problem kind declared in the source: the
// resolver's ProblemKind, the stale-rule StaleKind, the shims' IssueKind and
// the adapters' accounts.ProblemKind constants.
func problemKinds(t *testing.T) []string {
	t.Helper()
	var kinds []string
	scan := func(dir string, types ...string) {
		entries, err := os.ReadDir(dir)
		must(t, err)
		fset := token.NewFileSet()
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
			must(t, err)
			for _, d := range f.Decls {
				g, ok := d.(*ast.GenDecl)
				if !ok || g.Tok != token.CONST {
					continue
				}
				for _, sp := range g.Specs {
					vs := sp.(*ast.ValueSpec)
					typ := exprString(vs.Type)
					want := false
					for _, ty := range types {
						want = want || typ == ty
					}
					if !want {
						continue
					}
					for _, v := range vs.Values {
						if bl, ok := v.(*ast.BasicLit); ok && bl.Kind == token.STRING {
							s, _ := strconv.Unquote(bl.Value)
							kinds = append(kinds, s)
						}
					}
				}
			}
		}
	}
	scan("..", "ProblemKind", "StaleKind")
	scan(filepath.Join("..", "shims"), "IssueKind")
	scan(filepath.Join("..", "adapters"), "accounts.ProblemKind")
	if len(kinds) < 10 {
		t.Fatalf("found only %v", kinds)
	}
	return kinds
}

// Every problem kind Devpit can report has a troubleshooting page, and every
// page the table names exists in the docs site (skipped when the docs site
// is not there, as in a source tarball).
func TestEveryProblemKindHasADocsPage(t *testing.T) {
	for _, k := range problemKinds(t) {
		if DocsSlug(k, "") == "" {
			t.Errorf("the problem kind %q has no troubleshooting page in docsPages", k)
		}
	}
	if _, err := os.Stat(troubleshootingDir); errors.Is(err, os.ErrNotExist) {
		t.Skip("the docs site is not in this tree")
	}
	for slug := range DocsSlugs() {
		found := false
		for _, ext := range []string{".mdx", ".md"} {
			if _, err := os.Stat(filepath.Join(troubleshootingDir, slug+ext)); err == nil {
				found = true
			}
		}
		if !found {
			t.Errorf("the page %q is not in %s", slug, troubleshootingDir)
		}
	}
	if DocsSlug(string(accounts.ProblemEnvOverride), string(accounts.ToolClaude)) != "claude-config-dir-set-by-something-else" {
		t.Error("CLAUDE_CONFIG_DIR has its own page")
	}
}

// A problem carries its page in the JSON and prints it on its own line
// under the fix; a problem built without one still finds it by its kind.
func TestProblemsCarryTheirDocsLink(t *testing.T) {
	w := newWorld(t)
	w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	w.env["CLAUDE_CONFIG_DIR"] = `C:\stray`
	ts, err := w.s.Status(context.Background(), accounts.ToolClaude, w.work)
	must(t, err)
	var env *Problem
	for i, p := range ts.Problems {
		if p.Kind == string(accounts.ProblemEnvOverride) {
			env = &ts.Problems[i]
		}
	}
	if env == nil || env.Docs != DocsBase+"claude-config-dir-set-by-something-else" {
		t.Fatalf("problems = %+v", ts.Problems)
	}
	lines := strings.Split(env.Line(), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "  Fix: ") || lines[2] != "  Docs: "+env.Docs {
		t.Fatalf("line = %q", env.Line())
	}
	data, err := json.Marshal(ts.JSON())
	must(t, err)
	if !strings.Contains(string(data), `"docs":"`+env.Docs+`"`) {
		t.Fatalf("json = %s", data)
	}
	bare := Problem{Kind: string(shims.IssueNotOnPath), Message: "x"}
	if bare.Link() != DocsBase+"accounts-shim-not-first-on-path" {
		t.Fatalf("link = %q", bare.Link())
	}
}
