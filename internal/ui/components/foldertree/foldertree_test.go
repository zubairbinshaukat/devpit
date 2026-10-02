package foldertree

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

// update regenerates the golden files.
var update = flag.Bool("update", false, "rewrite the golden files")

// ctxAt builds the render context a screen would hand the tree.
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

// everywhere is the defaults row of the fixture.
func everywhere() []Chip {
	return []Chip{
		{Tool: "claude", Account: "default"},
		{Tool: "git", Account: "You"},
		{Tool: "github", Account: "default"},
		{Tool: "vercel", Account: "default"},
	}
}

// folders is a realistic set of rules: a work folder with a client inside
// it, two projects that only meet three folders up, a rule on a drive that
// is unplugged and one on a folder that is gone.
func folders() []Folder {
	return []Folder{
		{Path: `C:\Work`, Chips: []Chip{{"claude", "work"}, {"github", "work"}, {"git", "Work Name"}}},
		{Path: `C:\Work\client`, Chips: []Chip{{"git", "client"}}},
		{Path: `C:\Users\you\code\api`, Chips: []Chip{{"vercel", "api-team"}}},
		{Path: `C:\Users\you\code\web`, Chips: []Chip{{"vercel", "web"}}, Current: true},
		{Path: `D:\oss`, Chips: []Chip{{"claude", "oss"}, {"git", "oss"}}, State: StateOffline},
		{Path: `C:\Old\gone`, Chips: []Chip{{"claude", "old"}}, State: StateMissing},
	}
}

// tree is the fixture, built the way a screen would.
func tree() Model { return New(everywhere(), Build(folders())) }

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

// TestDemo prints the tree at a few widths and in every tier. Run it with
// go test ./internal/ui/components/foldertree -run Demo -v to eyeball it.
func TestDemo(t *testing.T) {
	for _, tier := range icons.Tiers() {
		for _, w := range []int{60, 80, 120} {
			m := tree().SetSize(w, 16)
			t.Logf("%s, %d columns:\n%s\n", tier, w, ansi.Strip(m.View(ctxAt(w, 24, icons.For(tier)))))
		}
	}
}

func TestBuildKeepsOnlyTheJointsItNeeds(t *testing.T) {
	roots := Build(folders())
	var got []string
	var walk func(ns []Node, depth int)
	walk = func(ns []Node, depth int) {
		for _, n := range ns {
			got = append(got, strings.Repeat("  ", depth)+n.Path)
			walk(n.Children, depth+1)
		}
	}
	walk(roots, 0)
	want := []string{
		`C:\Old\gone`,
		`C:\Users\you\code`,
		`  C:\Users\you\code\api`,
		`  C:\Users\you\code\web`,
		`C:\Work`,
		`  C:\Work\client`,
		`D:\oss`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Build made\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestBuildIgnoresCaseAndSlashes(t *testing.T) {
	roots := Build([]Folder{
		{Path: `c:/work/a`, Chips: []Chip{{"git", "a"}}},
		{Path: `C:\Work\B\`, Chips: []Chip{{"git", "b"}}},
	})
	if len(roots) != 1 || len(roots[0].Children) != 2 {
		t.Fatalf("one joint with two children expected, got %+v", roots)
	}
	if got := roots[0].Path; !strings.EqualFold(strings.ReplaceAll(got, "/", `\`), `C:\work`) {
		t.Fatalf("joint path %q", got)
	}
}

func TestBuildHandlesShares(t *testing.T) {
	got := segments(`\\nas\share\team\docs`)
	names := make([]string, len(got))
	for i, s := range got {
		names[i] = s.name
	}
	if strings.Join(names, "|") != `\\nas\share|team|docs` {
		t.Fatalf("segments = %q", names)
	}
	if got[2].path != `\\nas\share\team\docs` {
		t.Fatalf("last segment path %q", got[2].path)
	}
}

// A screen that builds its own tree gets chains of empty folders folded and
// names written relative to the node above.
func TestNewFoldsChainsAndNamesRelative(t *testing.T) {
	roots := []Node{{
		Path: `C:\Users`, Children: []Node{{
			Path: `C:\Users\you`, Children: []Node{{
				Path: `C:\Users\you\code`, Chips: []Chip{{"git", "you"}},
				Children: []Node{{Path: `C:\Users\you\code\src\app`, Chips: []Chip{{"git", "app"}}}},
			}},
		}},
	}}
	out := ansi.Strip(New(nil, roots).SetSize(100, 0).View(ctxAt(100, 30, icons.Unicode())))
	if !strings.Contains(out, `├─ ▾ C:\Users\you\code`) && !strings.Contains(out, `└─ ▾ C:\Users\you\code`) {
		t.Fatalf("the empty chain should fold into one row:\n%s", out)
	}
	if !strings.Contains(out, `src\app`) || strings.Contains(out, `code\src\app`) {
		t.Fatalf("a child should be named relative to its parent:\n%s", out)
	}
}

func TestGuidesAndMarks(t *testing.T) {
	out := ansi.Strip(tree().SetSize(120, 0).View(ctxAt(120, 30, icons.Unicode())))
	for _, want := range []string{
		"Everywhere", `├─ ▾ C:\Work`, `│  └─   client`, `└─   D:\oss`,
		"! drive not connected", "✗ folder not found", "web (here)",
		"claude work · github work · git Work Name",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	ascii := ansi.Strip(tree().SetSize(120, 0).View(ctxAt(120, 30, icons.ASCII())))
	for _, r := range ascii {
		if r > 0x7F && !strings.ContainsRune("●◆▲", r) {
			t.Fatalf("the ascii tier drew %q:\n%s", r, ascii)
		}
	}
	if !strings.Contains(ascii, "|- v C:\\Work") || !strings.Contains(ascii, "`- ") {
		t.Fatalf("ascii guides missing:\n%s", ascii)
	}
}

func TestCursorStartsOnTheCurrentFolder(t *testing.T) {
	id, _, ok := tree().Selected()
	if !ok || id != `C:\Users\you\code\web` {
		t.Fatalf("cursor starts on %q, want the current folder", id)
	}
	m := New(everywhere(), nil)
	if id, _, _ := m.Selected(); id != EverywhereID {
		t.Fatalf("with no current folder the cursor should be on Everywhere, got %q", id)
	}
}

func TestFoldWithArrowsAndSpace(t *testing.T) {
	m, ok := tree().Select(`C:\Work`)
	if !ok {
		t.Fatal("Select could not find C:\\Work")
	}
	n := m.VisibleCount()
	m, _ = m.Update(special(tea.KeyLeft))
	if m.VisibleCount() != n-1 {
		t.Fatalf("← should fold C:\\Work: %d rows, want %d", m.VisibleCount(), n-1)
	}
	out := ansi.Strip(m.SetSize(100, 0).View(ctxAt(100, 30, icons.Unicode())))
	if !strings.Contains(out, `▸ C:\Work +1`) {
		t.Fatalf("a folded node should say how much it hides:\n%s", out)
	}
	// ← again climbs to the parent, Everywhere.
	m, cmd := m.Update(special(tea.KeyLeft))
	if id, _, _ := m.Selected(); id != EverywhereID {
		t.Fatalf("← on a folded node should climb to its parent, got %q", id)
	}
	if cmd == nil {
		t.Fatal("moving the cursor should report the selection")
	}
	if sel, ok := cmd().(SelectedMsg); !ok || sel.ID != EverywhereID {
		t.Fatalf("got %#v", cmd())
	}
	m, _ = m.Select(`C:\Work`)
	m, _ = m.Update(press(" "))
	if m.VisibleCount() != n {
		t.Fatalf("space should unfold again: %d rows, want %d", m.VisibleCount(), n)
	}
	m, _ = m.Update(special(tea.KeyRight)) // already open: steps into it
	if id, _, _ := m.Selected(); id != `C:\Work\client` {
		t.Fatalf("→ on an open node should step to its first child, got %q", id)
	}
}

func TestEverywhereNeverFolds(t *testing.T) {
	m := tree()
	m, _ = m.Update(special(tea.KeyHome))
	n := m.VisibleCount()
	m, _ = m.Update(press(" "))
	m, _ = m.Update(special(tea.KeyLeft))
	if m.VisibleCount() != n {
		t.Fatal("Everywhere folded")
	}
}

func TestEnterOpens(t *testing.T) {
	m, _ := tree().Select(`D:\oss`)
	_, cmd := m.Update(special(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	if got, ok := cmd().(OpenMsg); !ok || got.Path != `D:\oss` {
		t.Fatalf("enter produced %#v", cmd())
	}
}

func TestFilterNarrowsAndKeepsAncestors(t *testing.T) {
	m := tree()
	m, _ = m.Update(press("/"))
	if !m.Filtering() {
		t.Fatal("/ did not open the filter")
	}
	for _, r := range "client" {
		m, _ = m.Update(press(string(r)))
	}
	if m.VisibleCount() != 3 { // Everywhere, C:\Work, client
		t.Fatalf("filter left %d rows, want 3", m.VisibleCount())
	}
	if id, _, _ := m.Selected(); id != `C:\Work\client` {
		t.Fatalf("the cursor should land on the match, got %q", id)
	}
	m, _ = m.Update(special(tea.KeyEnter))
	out := ansi.Strip(m.SetSize(80, 10).View(ctxAt(80, 24, icons.Unicode())))
	if !strings.Contains(out, "/client  1 folder") {
		t.Fatalf("a kept filter should say so:\n%s", out)
	}
	m, _ = m.Update(press("/"))
	m, _ = m.Update(special(tea.KeyEscape))
	if m.Filter() != "" || m.VisibleCount() != tree().VisibleCount() {
		t.Fatal("esc should clear the filter")
	}
	// Either slash finds a path.
	m, _ = m.Update(press("/"))
	for _, r := range "you/code" {
		m, _ = m.Update(press(string(r)))
	}
	if m.VisibleCount() != 4 {
		t.Fatalf("a forward-slash filter left %d rows, want 4", m.VisibleCount())
	}
}

func TestEmptyTreeSaysSo(t *testing.T) {
	for _, tier := range icons.Tiers() {
		out := New(nil, nil).SetSize(60, 6).View(ctxAt(60, 24, icons.For(tier)))
		plain := ansi.Strip(out)
		if !strings.Contains(plain, "no folder rules yet") || !strings.Contains(plain, "each tool's own default") {
			t.Fatalf("an empty tree should explain itself:\n%s", plain)
		}
		checkWidth(t, out, 60)
	}
	one := New(everywhere(), Build(folders()[:1])).SetSize(60, 6)
	if out := ansi.Strip(one.View(ctxAt(60, 24, icons.Unicode()))); !strings.Contains(out, `C:\Work`) {
		t.Fatalf("a one-folder tree lost it:\n%s", out)
	}
}

// TestEveryWidthFits is the layout contract: at every width from 60 to 200,
// in every tier, no line is wider than the tree, every folder is named, and
// every rule is on screen or counted in a "+n".
func TestEveryWidthFits(t *testing.T) {
	for _, tier := range icons.Tiers() {
		for w := 60; w <= 200; w++ {
			out := tree().SetSize(w, 0).View(ctxAt(w, 40, icons.For(tier)))
			checkWidth(t, out, w)
			plain := ansi.Strip(out)
			for _, name := range []string{"Everywhere", "client", "api", "web", "oss", "gone", "code"} {
				if !strings.Contains(plain, name) {
					t.Fatalf("%s at %d lost %q:\n%s", tier, w, name, plain)
				}
			}
			for _, acc := range []string{"api-team", "client", "oss"} {
				if !strings.Contains(plain, acc) {
					t.Fatalf("%s at %d lost the rule %q:\n%s", tier, w, acc, plain)
				}
			}
		}
	}
}

func TestLongNamesAndManyChips(t *testing.T) {
	var chips []Chip
	for i := range 14 {
		chips = append(chips, Chip{Tool: fmt.Sprintf("tool%d", i), Account: "account-" + strings.Repeat("x", i)})
	}
	deep := `C:\` + strings.Repeat(`very long folder name\`, 6) + `end`
	m := New(chips, Build([]Folder{
		{Path: deep, Chips: chips},
		{Path: deep + `\inner\漢字のフォルダ`, Chips: []Chip{{"git", "名前"}}},
	}))
	for _, tier := range icons.Tiers() {
		for _, w := range []int{60, 80, 133, 200} {
			out := m.SetSize(w, 0).View(ctxAt(w, 40, icons.For(tier)))
			checkWidth(t, out, w)
			if !strings.Contains(ansi.Strip(out), "+") {
				t.Fatalf("overflowing rules should be counted:\n%s", ansi.Strip(out))
			}
		}
	}
}

func TestScrollingKeepsTheCursorOnScreen(t *testing.T) {
	ctx := ctxAt(80, 24, icons.Unicode())
	m := tree().SetSize(80, 6)
	m, _ = m.Update(special(tea.KeyHome))
	for range m.VisibleCount() {
		out := ansi.Strip(m.View(ctx))
		if n := len(lines(out)); n > 6 {
			t.Fatalf("a 6-line tree drew %d lines:\n%s", n, out)
		}
		_, path, _ := m.Selected()
		if path != "" && !strings.Contains(out, path[strings.LastIndexAny(path, `\/`)+1:]) {
			t.Fatalf("the selected folder %s is off screen:\n%s", path, out)
		}
		m, _ = m.Update(press("j"))
	}
}

// Line 0 is the heading; with every row on one line at 120 columns, line
// n is visible row n-1.
func TestClickSelectsOpensAndFolds(t *testing.T) {
	ctx := ctxAt(120, 30, icons.Unicode())
	m := tree().SetSize(120, 0)
	m, _ = m.Update(special(tea.KeyHome))

	// Row 3 is C:\Users\you\code (Everywhere, C:\Old\gone, then it).
	m, cmd := m.Click(ctx, 40, 3)
	if id, _, _ := m.Selected(); id != `C:\Users\you\code` {
		t.Fatalf("click selected %q", id)
	}
	if cmd == nil {
		t.Fatal("a click that moves the selection should report it")
	}
	if _, cmd = m.Click(ctx, 40, 3); cmd == nil {
		t.Fatal("a click on the selected row should open it")
	} else if _, ok := cmd().(OpenMsg); !ok {
		t.Fatalf("got %#v", cmd())
	}

	// The fold arrow of a depth-1 node is at column 4+3 = 7; one cell
	// either side counts too.
	n := m.VisibleCount()
	m, _ = m.Click(ctx, 8, 3)
	if m.VisibleCount() != n-2 {
		t.Fatalf("a click on the arrow should fold: %d rows, want %d", m.VisibleCount(), n-2)
	}
	m, _ = m.Click(ctx, 6, 3)
	if m.VisibleCount() != n {
		t.Fatal("a second click on the arrow should unfold")
	}
	if _, cmd = m.Click(ctx, 10, 0); cmd != nil {
		t.Fatal("the heading is not a row")
	}
}

func TestPointerHoverAndWheel(t *testing.T) {
	ctx := ctxAt(120, 30, icons.Unicode())
	m := tree().SetSize(120, 0)
	m, _ = m.Update(special(tea.KeyHome))
	const top = 1
	m, _, handled := m.Pointer(ctx, tea.MouseMotionMsg{X: 20, Y: ctx.BodyTop + top + 2}, top)
	if !handled || m.hover != 1 || m.Cursor() != 0 {
		t.Fatalf("hover = %d cursor = %d, want hover on row 1 and the cursor still", m.hover, m.Cursor())
	}
	m, _, _ = m.Pointer(ctx, tea.MouseWheelMsg{Button: tea.MouseWheelDown}, top)
	if m.Cursor() != 1 {
		t.Fatalf("wheel moved the cursor to %d", m.Cursor())
	}
	m, _, _ = m.Pointer(ctx, tea.MouseClickMsg{X: 20, Y: ctx.BodyTop + top + 3, Button: tea.MouseLeft}, top)
	if m.Cursor() != 2 {
		t.Fatalf("pointer click moved the cursor to %d", m.Cursor())
	}
	m, _ = m.Update(press("/"))
	if _, _, handled = m.Pointer(ctx, tea.MouseWheelMsg{Button: tea.MouseWheelDown}, top); handled {
		t.Fatal("the mouse should be left alone while the filter has the keyboard")
	}
}

func TestThemesAndTiersRender(t *testing.T) {
	for _, v := range []theme.Variant{theme.VariantAqua, theme.VariantMono} {
		for _, dark := range []bool{true, false} {
			for _, tier := range icons.Tiers() {
				ctx := ctxAt(80, 24, icons.For(tier))
				ctx.Theme = theme.Of(v, dark)
				checkWidth(t, tree().SetSize(80, 18).View(ctx), 80)
			}
		}
	}
}

// TestGolden pins the tree at the body sizes an 80×24 and a 100×30 terminal
// leave, in the unicode and ascii tiers, with colour stripped.
func TestGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, size := range []struct{ w, h int }{{80, 24}, {100, 30}} {
		for _, tier := range []icons.Tier{icons.TierUnicode, icons.TierASCII} {
			name := fmt.Sprintf("foldertree_%dx%d_%s.golden", size.w, size.h, tier)
			t.Run(name, func(t *testing.T) {
				ctx := ctxAt(size.w, size.h, icons.For(tier))
				m := tree().SetSize(size.w, ctx.BodyHeight)
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
		t.Fatalf("reading %s: %v (run go test ./internal/ui/components/foldertree -update)", path, err)
	}
	if got != string(want) {
		t.Errorf("%s changed.\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}
