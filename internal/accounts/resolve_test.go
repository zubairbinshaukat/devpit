package accounts

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// fixture is a store with three Claude accounts, a GitHub account, and
// nested rules that set different tools.
func fixture(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	for _, a := range []Account{
		{Tool: ToolClaude, Name: "work", Email: "zubair@work.com", Dir: `C:\Users\z\.devpit\accounts\claude\work`},
		{Tool: ToolClaude, Name: "oss", Email: "zubair@proton.me", Dir: `C:\Users\z\.devpit\accounts\claude\oss`},
		{Tool: ToolClaude, Name: "personal", Email: "zubair@gmail.com", Dir: `C:\Users\z\.devpit\accounts\claude\personal`},
		{Tool: ToolGitHub, Name: "work", Label: "zubair-work"},
	} {
		if err := s.AddAccount(a); err != nil {
			t.Fatal(err)
		}
	}
	must(t, s.SetRule(`C:\Work`, ToolClaude, "work"))
	must(t, s.SetRule(`C:\Work`, ToolGitHub, "work"))
	must(t, s.SetRule(`C:\Work\oss`, ToolClaude, "oss"))
	must(t, s.SetRule(`C:\Work\gh-only`, ToolGitHub, "default"))
	must(t, s.SetRule(`D:\`, ToolClaude, "personal"))
	must(t, s.SetRule(`\\nas\projects`, ToolClaude, "oss"))
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestResolveNearestRuleWinsPerTool(t *testing.T) {
	s := fixture(t)
	cases := []struct {
		tool   Tool
		folder string
		base   string
		want   string
		why    string
	}{
		{ToolClaude, `C:\Work`, "", "work", `folder rule: C:\Work`},
		{ToolClaude, `C:\Work\api\src`, "", "work", `folder rule: C:\Work`},
		{ToolClaude, `c:\work\API\`, "", "work", `folder rule: C:\Work`},
		{ToolClaude, `C:/Work/api`, "", "work", `folder rule: C:\Work`},
		{ToolClaude, `\\?\C:\Work\api`, "", "work", `folder rule: C:\Work`},
		{ToolClaude, `C:\Work\oss\lib`, "", "oss", `folder rule: C:\Work\oss`},
		{ToolClaude, `C:\Workshop`, "", "default", "everywhere"},
		{ToolClaude, `C:\Workshop\x`, "", "default", "everywhere"},
		{ToolClaude, `C:\`, "", "default", "everywhere"},
		{ToolClaude, `d:\anything\deep`, "", "personal", `folder rule: D:\`},
		{ToolClaude, `D:\`, "", "personal", `folder rule: D:\`},
		{ToolClaude, `\\NAS\Projects\site`, "", "oss", `folder rule: \\nas\projects`},
		{ToolClaude, `\\?\UNC\nas\projects\site`, "", "oss", `folder rule: \\nas\projects`},
		{ToolClaude, `\\nas\elsewhere`, "", "default", "everywhere"},
		{ToolClaude, `api`, `C:\Work`, "work", `folder rule: C:\Work`},
		{ToolClaude, `..\oss`, `C:\Work\api`, "oss", `folder rule: C:\Work\oss`},
		// A rule that sets only GitHub does not move Claude.
		{ToolClaude, `C:\Work\gh-only\x`, "", "work", `folder rule: C:\Work`},
		{ToolGitHub, `C:\Work\gh-only\x`, "", "default", `folder rule: C:\Work\gh-only`},
		{ToolGitHub, `C:\Work\oss`, "", "work", `folder rule: C:\Work`},
		// Non-existent folders resolve like any other.
		{ToolClaude, `C:\Work\not\made\yet`, "", "work", `folder rule: C:\Work`},
		{ToolVercel, `C:\Work`, "", "default", "everywhere"},
	}
	for _, c := range cases {
		r, err := Resolve(s, c.tool, c.folder, ResolveOptions{Base: c.base})
		if err != nil {
			t.Errorf("Resolve(%s, %q): %v", c.tool, c.folder, err)
			continue
		}
		if r.Account.Name != c.want || r.Why() != c.why {
			t.Errorf("Resolve(%s, %q) = %s (%s), want %s (%s)", c.tool, c.folder, r.Account.Name, r.Why(), c.want, c.why)
		}
	}
}

func TestResolveChainOuterToInner(t *testing.T) {
	s := fixture(t)
	r, err := Resolve(s, ToolClaude, `C:\Work\oss\x`, ResolveOptions{})
	must(t, err)
	if len(r.Chain) != 2 {
		t.Fatalf("chain = %+v", r.Chain)
	}
	if r.Chain[0].Folder != `C:\Work` || r.Chain[0].Won || r.Chain[1].Folder != `C:\Work\oss` || !r.Chain[1].Won {
		t.Fatalf("chain = %+v", r.Chain)
	}
}

func TestEverywhereAndDefault(t *testing.T) {
	s := fixture(t)
	r, _ := Resolve(s, ToolClaude, `E:\x`, ResolveOptions{})
	if !r.IsDefault() || r.Reason != ReasonEverywhere {
		t.Fatalf("got %v", r)
	}
	must(t, s.SetEverywhere(ToolClaude, "personal"))
	r, _ = Resolve(s, ToolClaude, `E:\x`, ResolveOptions{})
	if r.Account.Name != "personal" || r.Why() != "everywhere" {
		t.Fatalf("got %v", r)
	}
	// The nearer folder rule still wins over everywhere.
	r, _ = Resolve(s, ToolClaude, `C:\Work`, ResolveOptions{})
	if r.Account.Name != "work" {
		t.Fatalf("got %v", r)
	}
}

func TestRuleForAMissingAccountIsSkippedAndReported(t *testing.T) {
	s := fixture(t)
	// Hand-edit: the rule on C:\Work\oss names an account that is gone.
	s.Accounts = s.Accounts[:0:0]
	must(t, s.AddAccount(Account{Tool: ToolClaude, Name: "work", Dir: `C:\a`}))
	r, err := Resolve(s, ToolClaude, `C:\Work\oss\x`, ResolveOptions{})
	must(t, err)
	if r.Account.Name != "work" || r.RuleFolder != `C:\Work` {
		t.Fatalf("want fall through to C:\\Work, got %v", r)
	}
	if len(r.Problems) != 1 || r.Problems[0].Kind != ProblemMissingAccount || r.Problems[0].Account != "oss" {
		t.Fatalf("problems = %+v", r.Problems)
	}
	if !r.Chain[1].Skipped || r.Chain[1].Won || !r.Chain[0].Won {
		t.Fatalf("chain = %+v", r.Chain)
	}
	// Everywhere naming a missing account: default, with a problem.
	s.Everywhere[ToolClaude] = "gone"
	r, _ = Resolve(s, ToolClaude, `E:\`, ResolveOptions{})
	if !r.IsDefault() || len(r.Problems) != 1 || r.Problems[0].Kind != ProblemEverywhereMissing {
		t.Fatalf("got %v %+v", r, r.Problems)
	}
}

func TestResolvedPathIsTriedWhenTheGivenOneMatchesNothing(t *testing.T) {
	s := fixture(t)
	// S: is a subst drive (or J:\link a junction) for C:\Work\api.
	resolve := func(p string) (string, error) {
		switch {
		case FolderContains(`S:\`, p):
			return `C:\Work\api` + strings.TrimPrefix(p, `S:`), nil
		case FolderContains(`C:\Work\link`, p):
			return `C:\Elsewhere` + strings.TrimPrefix(p, `C:\Work\link`), nil
		}
		return p, nil
	}
	r, err := Resolve(s, ToolClaude, `S:\src`, ResolveOptions{RealPath: resolve})
	must(t, err)
	if r.Account.Name != "work" || r.Via != ViaResolved || r.ResolvedFolder != `C:\Work\api\src` {
		t.Fatalf("got %+v", r)
	}
	if !strings.Contains(r.Why(), `through C:\Work\api\src`) {
		t.Fatalf("why = %q", r.Why())
	}
	// The path as given wins when it matches: a junction inside C:\Work
	// pointing elsewhere still follows C:\Work's rule.
	r, _ = Resolve(s, ToolClaude, `C:\Work\link\x`, ResolveOptions{RealPath: resolve})
	if r.Account.Name != "work" || r.Via != ViaGiven {
		t.Fatalf("got %+v", r)
	}
	// A failing RealPath is ignored.
	r, err = Resolve(s, ToolClaude, `S:\src`, ResolveOptions{RealPath: func(string) (string, error) { return "", errors.New("no") }})
	if err != nil || !r.IsDefault() {
		t.Fatalf("got %v %v", r, err)
	}
}

func TestResolveRejectsUnusableFolders(t *testing.T) {
	s := fixture(t)
	if _, err := Resolve(s, ToolClaude, "relative", ResolveOptions{}); !errors.Is(err, ErrRelativePath) {
		t.Fatalf("err = %v", err)
	}
	if _, err := Resolve(nil, ToolClaude, `C:\x`, ResolveOptions{}); err != nil {
		t.Fatalf("nil store: %v", err)
	}
}

func TestResolveAllListsEveryToolInOrder(t *testing.T) {
	rs, err := ResolveAll(fixture(t), `C:\Work`, ResolveOptions{})
	must(t, err)
	if len(rs) != 8 || rs[0].Tool != ToolClaude || rs[2].Tool != ToolGitHub || rs[2].Account.Name != "work" {
		t.Fatalf("got %v", rs)
	}
}

// TestResolveIsFast guards the shim's budget: parsing a realistic store and
// resolving must stay far under a millisecond or two, so the shim's total
// overhead (about 30 ms, most of it Windows starting two processes) is not
// eaten by Devpit's own work.
func TestResolveIsFast(t *testing.T) {
	data := benchStore(t)
	const n = 200
	start := time.Now()
	for i := 0; i < n; i++ {
		s, err := Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Resolve(s, ToolClaude, `C:\Work\p17\src\deep\er`, ResolveOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	per := time.Since(start) / n
	t.Logf("parse + resolve with 60 rules: %v per call", per)
	if per > 5*time.Millisecond {
		t.Fatalf("parse + resolve took %v per call; the shim's budget is a few milliseconds", per)
	}
}

func BenchmarkParseAndResolve(b *testing.B) {
	data := benchStore(b)
	b.ReportAllocs()
	for b.Loop() {
		s, err := Parse(data)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := Resolve(s, ToolClaude, `C:\Work\p17\src\deep\er`, ResolveOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func benchStore(tb testing.TB) []byte {
	tb.Helper()
	s := NewStore()
	for i := 0; i < 6; i++ {
		name := "acct" + string(rune('a'+i))
		if err := s.AddAccount(Account{Tool: ToolClaude, Name: name, Email: name + "@x.com", Dir: `C:\Users\z\.devpit\accounts\claude\` + name}); err != nil {
			tb.Fatal(err)
		}
	}
	for i := 0; i < 60; i++ {
		folder := `C:\Work\p` + itoa(i)
		if err := s.SetRule(folder, ToolClaude, "acct"+string(rune('a'+i%6))); err != nil {
			tb.Fatal(err)
		}
	}
	data, err := Encode(s)
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}
