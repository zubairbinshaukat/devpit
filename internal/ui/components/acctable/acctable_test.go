package acctable

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// update regenerates the golden files. Every package's tests are a separate
// binary, so this flag never meets the app package's flag of the same name.
var update = flag.Bool("update", false, "rewrite the golden files")

// ctxAt builds the render context a screen would hand the table: a terminal
// of w×h with the body the header and footer leave.
func ctxAt(w, h int, ic icons.Set) uictx.Context {
	return uictx.Context{
		Theme:      theme.For(true),
		Icons:      ic,
		Width:      w,
		Height:     h,
		BodyHeight: h - 6,
		BodyTop:    4,
	}
}

// sample is the Accounts page from the plan, with fictional emails, a cached
// row being re-checked, an expired login and a caption on the first row.
func sample() []Row { return sampleFor(icons.Unicode()) }

// sampleFor is sample with the caption's arrow in the tier's own spelling,
// the way a screen builds it.
func sampleFor(ic icons.Set) []Row {
	return []Row{
		{
			ID: "claude", Tool: "Claude Code",
			Name: "work", Detail: "(you@work.com)",
			Why: "folder rule:", WhyPath: `C:\Projects\quiz-slayer`, WhyKind: WhyFolderRule,
			Caption: `C:\Projects ` + ic.Arrow + ` C:\Projects\quiz-slayer (won)`,
		},
		{
			ID: "git", Tool: "Git", Icon: "git",
			Name: "You", Detail: "<you.personal@gmail.com>",
			Why: "everywhere", WhyKind: WhyEverywhere,
		},
		{
			ID: "github", Tool: "GitHub", Icon: "git", State: StateLoading,
			Name: "default", Detail: "(you-on-github)",
			Why: "everywhere", WhyKind: WhyEverywhere,
		},
		{
			ID: "vercel", Tool: "Vercel", Icon: "globe", State: StateExpired,
			Name: "default", Detail: "(you@gmail.com)",
			Why: "everywhere", WhyKind: WhyEverywhere, Note: "sign in again",
		},
		{
			ID: "cloudflare", Tool: "Cloudflare", Icon: "globe",
			Name: "default", Detail: "(you@gmail.com)",
			Why: "everywhere", WhyKind: WhyEverywhere, Note: "beta",
		},
		{
			ID: "convex", Tool: "Convex", Icon: "package",
			Name: "project:", Detail: "quiz-slayer",
			Why: "set by this project's .env.local", WhyKind: WhyProjectFile,
		},
		{ID: "firebase", Tool: "Firebase", Icon: "globe", State: StateSignedOut},
		{ID: "supabase", Tool: "Supabase", Icon: "package", State: StateNotInstalled},
	}
}

// press builds a printable key press.
func press(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: []rune(s)[0], Text: s} }

// special builds a named key press.
func special(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

// lines strips a frame and splits it.
func lines(s string) []string { return strings.Split(ansi.Strip(s), "\n") }

// checkWidth fails for any line wider than w cells.
func checkWidth(t *testing.T, out string, w int) {
	t.Helper()
	for i, ln := range lines(out) {
		if got := ansi.StringWidth(ln); got > w {
			t.Errorf("line %d is %d cells, want at most %d:\n%q", i, got, w, ln)
		}
	}
}

// TestDemo prints the table at a few widths and in every tier. Run it with
// go test ./internal/ui/components/acctable -run Demo -v to eyeball a change.
func TestDemo(t *testing.T) {
	for _, tier := range icons.Tiers() {
		for _, w := range []int{60, 80, 120} {
			m := New(sampleFor(icons.For(tier))).SetFrame(3).SetSize(w, 14)
			out := ansi.Strip(m.View(ctxAt(w, 24, icons.For(tier))))
			t.Logf("%s, %d columns:\n%s\n", tier, w, out)
		}
	}
}

func TestShortenPathKeepsDriveAndLastFolder(t *testing.T) {
	cases := []struct {
		path string
		w    int
		ell  string
		want string
	}{
		{`C:\Projects\quiz-slayer`, 40, "…", `C:\Projects\quiz-slayer`},
		{`C:\Projects\quiz-slayer`, 20, "…", `C:\Proj…\quiz-slayer`},
		{`C:\Projects\quiz-slayer`, 16, "…", `C:\…\quiz-slayer`},
		{`C:\Projects\quiz-slayer`, 13, "…", `…\quiz-slayer`},
		{`C:\Projects\quiz-slayer`, 8, "…", `quiz-sl…`},
		{`C:\Projects\quiz-slayer`, 22, "...", `C:\Proj...\quiz-slayer`},
		{`D:/work/clients/acme/api/`, 18, "…", `D:/work/clien…/api`},
		{`\\nas\share\team\docs`, 16, "…", `\\nas\shar…\docs`},
		{`C:\Projects`, 8, "…", `Projects`}, // no middle: the drive goes first
		{`C:\Projects`, 6, "…", `Proje…`},
		{`C:\プロジェクト\作業\quiz`, 15, "…", `C:\プロジ…\quiz`}, // wide characters are never split
		{`C:\プロジェクト\作業\quiz`, 14, "…", `C:\プロ…\quiz`},
	}
	for _, c := range cases {
		got := ShortenPath(c.path, c.w, c.ell)
		if got != c.want {
			t.Errorf("ShortenPath(%q, %d) = %q, want %q", c.path, c.w, got, c.want)
		}
		if w := ansi.StringWidth(got); w > c.w {
			t.Errorf("ShortenPath(%q, %d) is %d cells", c.path, c.w, w)
		}
	}
}

func TestShortenEmailKeepsTheDomain(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"(you@work.com)", 20, "(you@work.com)"},
		{"(you.with.a.long.name@work.com)", 18, "(you.wi…@work.com)"},
		{"<you.with.a.long.name@gmail.com>", 13, "<…@gmail.com>"},
		{"<you.with.a.long.name@gmail.com>", 12, ""}, // the domain no longer fits
		{"(you-on-github)", 10, "(you-on-g…"},
		{"(you@work.com)", 5, ""},
	}
	for _, c := range cases {
		got := ShortenEmail(c.in, c.w, "…")
		if got != c.want {
			t.Errorf("ShortenEmail(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
		if ansi.StringWidth(got) > c.w {
			t.Errorf("ShortenEmail(%q, %d) = %q is too wide", c.in, c.w, got)
		}
	}
}

// whyForms is every string the Why of r may legitimately be drawn as at
// some width: whole, or with the path shortened by ShortenPath. The test
// below accepts any of them, as long as one is there.
func whyPresent(out string, r Row, ell string) bool {
	flat := strings.Join(strings.Fields(out), " ")
	words, path := r.Why, r.WhyPath
	if path == "" {
		words, path = splitWhy(r.Why)
	}
	if words != "" && !strings.Contains(flat, words) {
		return false
	}
	if path == "" {
		return true
	}
	if strings.Contains(flat, path) {
		return true
	}
	_, _, sep, last := splitPath(path)
	return strings.Contains(flat, ell+sep+last) || strings.Contains(flat, last[:min(len(last), 4)])
}

// TestWhyIsNeverDropped is the page's contract: at every width from 60 to
// 200, in every tier, every line fits and every row's reason is on screen,
// whole or with its path shortened in the middle.
func TestWhyIsNeverDropped(t *testing.T) {
	for _, tier := range icons.Tiers() {
		ell := "…"
		if tier == icons.TierASCII {
			ell = "..."
		}
		for w := 60; w <= 200; w++ {
			ctx := ctxAt(w, 40, icons.For(tier))
			m := New(sample()).SetSize(w, 0)
			out := m.View(ctx)
			checkWidth(t, out, w)
			for _, r := range sample() {
				if r.Why == "" {
					continue
				}
				if !whyPresent(ansi.Strip(out), r, ell) {
					t.Fatalf("%s at %d columns lost the reason of %s:\n%s", tier, w, r.Tool, ansi.Strip(out))
				}
			}
		}
	}
}

// TestGivingWayOrder pins what goes first as the table narrows: the path in
// the middle, then the email, and only then a second line.
func TestGivingWayOrder(t *testing.T) {
	ctx := func(w int) uictx.Context { return ctxAt(w, 40, icons.Unicode()) }
	rows := []Row{{
		ID: "claude", Tool: "Claude Code", Name: "work", Detail: "(you.someone@work.com)",
		Why: `folder rule: C:\Projects\clients\quiz-slayer`, WhyKind: WhyFolderRule,
	}}
	m := New(rows)

	wide := ansi.Strip(m.SetSize(120, 0).View(ctx(120)))
	if !strings.Contains(wide, `C:\Projects\clients\quiz-slayer`) || !strings.Contains(wide, "(you.someone@work.com)") {
		t.Fatalf("at 120 columns nothing should give way:\n%s", wide)
	}

	// Narrow enough that the path must shorten, but the email still fits.
	mid := ansi.Strip(m.SetSize(84, 0).View(ctx(84)))
	if strings.Contains(mid, `C:\Projects\clients\quiz-slayer`) {
		t.Fatalf("at 84 columns the path should be shortened:\n%s", mid)
	}
	if !strings.Contains(mid, `…\quiz-slayer`) || !strings.Contains(mid, `C:\`) {
		t.Fatalf("the shortened path should keep its drive and last folder:\n%s", mid)
	}
	if !strings.Contains(mid, "(you.someone@work.com)") {
		t.Fatalf("the email gave way before the path had finished shortening:\n%s", mid)
	}

	// Narrower: the path is at its floor, so the email shortens, the name
	// stays whole, and the row is still one line.
	tight := ansi.Strip(m.SetSize(66, 0).View(ctx(66)))
	if strings.Contains(tight, "(you.someone@work.com)") {
		t.Fatalf("at 66 columns the email should be shortened:\n%s", tight)
	}
	if !strings.Contains(tight, "work") || !strings.Contains(tight, `C:\…\quiz-slayer`) {
		t.Fatalf("the name and the floor of the path must survive:\n%s", tight)
	}
	if got := len(lines(tight)); got != 2 {
		t.Fatalf("at 66 columns the row should still be one line, got %d lines:\n%s", got, tight)
	}

	// Narrowest: only now does the reason wrap under the account.
	rows[0].Name = "a-very-long-account-name"
	narrow := ansi.Strip(New(rows).SetSize(60, 0).View(ctx(60)))
	if got := len(lines(narrow)); got != 3 {
		t.Fatalf("at 60 columns with a long name the reason should wrap, got %d lines:\n%s", got, narrow)
	}
	if !strings.Contains(narrow, "a-very-long-account-name") {
		t.Fatalf("the name must be kept whole:\n%s", narrow)
	}
	checkWidth(t, narrow, 60)
}

func TestStatesCarryAShapeAndAWord(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	out := ansi.Strip(New(sample()).SetSize(100, 0).View(ctx))
	for _, want := range []string{"✓ work", "! default", "expired", "· not signed in", "─ not installed"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "checking") {
		t.Errorf("a loading row with a cached account should not say checking:\n%s", out)
	}
	rows := []Row{{ID: "x", Tool: "Vercel", State: StateLoading}}
	out = ansi.Strip(New(rows).SetSize(100, 0).View(ctx))
	if !strings.Contains(out, "checking…") {
		t.Errorf("a loading row with nothing yet should say checking…:\n%s", out)
	}
}

// A row that just changed says so in words and keeps its place: the flag
// rides at the end of the Why column, so nothing to the left of it moves.
func TestFreshRowSaysUpdated(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	rows := sample()
	before := lines(New(rows).SetSize(100, 0).View(ctx))
	rows[4].Fresh = true // Cloudflare
	after := lines(New(rows).SetSize(100, 0).View(ctx))
	if len(before) != len(after) {
		t.Fatalf("fresh changed the table height from %d to %d", len(before), len(after))
	}
	row := after[6]
	if !strings.Contains(row, "Cloudflare") || !strings.Contains(row, "everywhere · beta · updated") {
		t.Fatalf("the fresh row should end with its note and the word updated:\n%s", strings.Join(after, "\n"))
	}
	if strings.Index(row, "everywhere") != strings.Index(before[6], "everywhere") {
		t.Fatal("fresh moved the Why column")
	}
	for i := range before {
		if i != 6 && before[i] != after[i] {
			t.Fatalf("fresh changed another line (%d):\n%s\n%s", i, before[i], after[i])
		}
	}
	// Colour is a bonus, not the message: the fresh account wears the
	// success hue on top of the word.
	styled := New(rows).SetSize(100, 0).View(ctx)
	if !strings.Contains(styled, ctx.Theme.Success.Bold(true).Render("default")) {
		t.Error("the fresh account should be drawn in the success style")
	}
}

func TestLoadingKeepsTheRowHeight(t *testing.T) {
	ctx := ctxAt(80, 24, icons.Unicode())
	rows := sample()
	before := New(rows).SetSize(80, 0).Height(ctx)
	for i := range rows {
		rows[i].State = StateLoading
	}
	after := New(rows).SetSize(80, 0).Height(ctx)
	if before != after {
		t.Fatalf("loading changed the table height from %d to %d", before, after)
	}
}

func TestEmptyAndSingleRow(t *testing.T) {
	ctx := ctxAt(60, 24, icons.ASCII())
	out := ansi.Strip(New(nil).SetSize(60, 6).View(ctx))
	if !strings.Contains(out, "No tools to show yet.") {
		t.Fatalf("an empty table should say so:\n%s", out)
	}
	m, cmd := New(nil).Update(special(tea.KeyEnter))
	if cmd != nil || m.Cursor() != 0 {
		t.Fatal("enter on an empty table should do nothing")
	}
	one := New(sample()[:1]).SetSize(60, 6)
	out = ansi.Strip(one.View(ctx))
	if !strings.Contains(out, "Claude Code") {
		t.Fatalf("a one-row table lost its row:\n%s", out)
	}
	checkWidth(t, out, 60)
}

func TestExtremelyLongNamesStayInside(t *testing.T) {
	long := strings.Repeat("very-long-", 20)
	rows := []Row{{
		ID: "x", Tool: "A tool with " + long, Name: long, Detail: "(" + long + "@example.com)",
		Why: "folder rule: C:\\" + strings.Repeat("deep\\", 30) + "end", WhyKind: WhyFolderRule,
		Note: long, Caption: long,
	}, {ID: "y", Tool: "漢字のツール", Name: "名前", Detail: "(メール@例え.jp)", Why: "everywhere"}}
	for _, tier := range icons.Tiers() {
		for _, w := range []int{60, 80, 133, 200} {
			out := New(rows).SetSize(w, 0).View(ctxAt(w, 40, icons.For(tier)))
			checkWidth(t, out, w)
		}
	}
}

func TestScrollingKeepsTheCursorOnScreen(t *testing.T) {
	ctx := ctxAt(80, 24, icons.Unicode())
	m := New(sample()).SetSize(80, 6)
	for range len(sample()) {
		m, _ = m.Update(press("j"))
		out := ansi.Strip(m.View(ctx))
		if n := len(lines(out)); n > 6 {
			t.Fatalf("a 6-line table drew %d lines:\n%s", n, out)
		}
		sel, _ := m.Selected()
		if !strings.Contains(out, sel.Tool) {
			t.Fatalf("the selected row %s is off screen:\n%s", sel.Tool, out)
		}
		if !strings.Contains(out, " of 8") {
			t.Fatalf("a scrolled table should say where it is:\n%s", out)
		}
	}
	if m.Cursor() != len(sample())-1 {
		t.Fatalf("j walked to %d, want the last row", m.Cursor())
	}
	m, _ = m.Update(special(tea.KeyHome))
	if m.Cursor() != 0 {
		t.Fatalf("home went to %d", m.Cursor())
	}
	m, _ = m.Update(special(tea.KeyEnd))
	if m.Cursor() != len(sample())-1 {
		t.Fatalf("end went to %d", m.Cursor())
	}
}

func TestEnterOpens(t *testing.T) {
	m := New(sample())
	m, _ = m.Update(special(tea.KeyDown))
	_, cmd := m.Update(special(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	if got, ok := cmd().(OpenMsg); !ok || got.ID != "git" || got.Index != 1 {
		t.Fatalf("enter produced %#v, want OpenMsg for git", cmd())
	}
}

func TestSetRowsKeepsTheSelectedTool(t *testing.T) {
	m := New(sample())
	m, _ = m.Update(special(tea.KeyDown))
	m, _ = m.Update(special(tea.KeyDown))
	rows := sample()
	rows[0], rows[2] = rows[2], rows[0]
	m = m.SetRows(rows)
	if r, _ := m.Selected(); r.ID != "github" {
		t.Fatalf("after SetRows the selection moved to %s", r.ID)
	}
}

// Row 0 is the heading. With the first row selected and captioned, row 1 is
// its main line, row 2 its caption, row 3 the next tool.
func TestRowAtFollowsTheDrawnLayout(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := New(sample()).SetSize(100, 0)
	cases := []struct {
		y, want int
		ok      bool
	}{{-1, 0, false}, {0, 0, false}, {1, 0, true}, {2, 0, true}, {3, 1, true}, {4, 2, true}, {10, 0, false}}
	for _, c := range cases {
		got, ok := m.RowAt(ctx, c.y)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("RowAt(%d) = %d,%v want %d,%v", c.y, got, ok, c.want, c.ok)
		}
	}
}

func TestClickSelectsThenOpens(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := New(sample()).SetSize(100, 0)

	m, cmd := m.Click(ctx, 3)
	if m.Cursor() != 1 || cmd != nil {
		t.Fatalf("first click: cursor=%d cmd=%v, want 1 and nothing opened", m.Cursor(), cmd)
	}
	// The caption moved with the selection, so git is now on line 2.
	m, cmd = m.Click(ctx, 2)
	if cmd == nil {
		t.Fatal("a click on the selected row should open it")
	}
	if got, ok := cmd().(OpenMsg); !ok || got.ID != "git" {
		t.Fatalf("second click produced %#v", cmd())
	}
	if _, cmd = m.Click(ctx, 0); cmd != nil {
		t.Fatal("a click on the heading should do nothing")
	}
}

func TestPointerUsesBodyRows(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := New(sample()).SetSize(100, 0)
	const top = 2 // two lines of the screen's own above the table
	y := ctx.BodyTop + top + 3
	m, _, handled := m.Pointer(ctx, tea.MouseClickMsg{X: 10, Y: y, Button: tea.MouseLeft}, top)
	if !handled || m.Cursor() != 1 {
		t.Fatalf("pointer click: handled=%v cursor=%d, want git selected", handled, m.Cursor())
	}
	// The caption moved with the selection: git is on line 2, github on 3.
	m, _, _ = m.Pointer(ctx, tea.MouseMotionMsg{X: 10, Y: ctx.BodyTop + top + 3}, top)
	if m.hover != 2 || m.Cursor() != 1 {
		t.Fatalf("hover moved the selection or missed: hover=%d cursor=%d", m.hover, m.Cursor())
	}
	out := ansi.Strip(m.View(ctx))
	if !strings.Contains(out, "  ▸ ") {
		t.Errorf("the hovered row should carry a quiet caret:\n%s", out)
	}
	m, _, _ = m.Pointer(ctx, tea.MouseWheelMsg{Button: tea.MouseWheelDown}, top)
	if m.Cursor() != 2 {
		t.Fatalf("wheel down moved the cursor to %d", m.Cursor())
	}
	if _, _, handled = m.Pointer(ctx, press("j"), top); handled {
		t.Fatal("a key is not the pointer's")
	}
}

func TestThemesAndTiersRender(t *testing.T) {
	for _, v := range []theme.Variant{theme.VariantAqua, theme.VariantMono} {
		for _, dark := range []bool{true, false} {
			for _, tier := range icons.Tiers() {
				ctx := ctxAt(80, 24, icons.For(tier))
				ctx.Theme = theme.Of(v, dark)
				out := New(sampleFor(icons.For(tier))).SetFrame(2).SetSize(80, 18).View(ctx)
				checkWidth(t, out, 80)
				if tier == icons.TierASCII {
					for _, r := range ansi.Strip(out) {
						if r > 0x7F && !strings.ContainsRune("●◆▲", r) {
							t.Fatalf("the ascii tier drew %q", r)
						}
					}
				}
			}
		}
	}
}

// TestGolden pins the table at the body sizes an 80×24 and a 100×30
// terminal leave, in the unicode and ascii tiers, with colour stripped.
func TestGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, size := range []struct{ w, h int }{{80, 24}, {100, 30}} {
		for _, tier := range []icons.Tier{icons.TierUnicode, icons.TierASCII} {
			name := fmt.Sprintf("acctable_%dx%d_%s.golden", size.w, size.h, tier)
			t.Run(name, func(t *testing.T) {
				ctx := ctxAt(size.w, size.h, icons.For(tier))
				m := New(sampleFor(icons.For(tier))).SetFrame(0).SetSize(size.w, ctx.BodyHeight)
				golden(t, name, ansi.Strip(m.View(ctx)))
			})
		}
	}
}

// golden compares got with a file under testdata, or rewrites it with
// -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v (run go test ./internal/ui/components/acctable -update)", path, err)
	}
	if got != string(want) {
		t.Errorf("%s changed.\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}
