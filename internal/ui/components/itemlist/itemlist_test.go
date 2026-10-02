package itemlist

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

// ctxAt builds the render context a screen would hand the list.
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

// share, copyOnly and sameList are the option sets the plan's table uses.
var (
	share    = []Mode{ModeShare, ModeCopy, ModeSkip}
	copyOnly = []Mode{ModeCopy, ModeSkip}
	sameList = []Mode{ModeSameList, ModeSkip}
)

// sample is the Claude setup list from the plan, section 6.
func sample() []Row {
	return []Row{
		{
			ID: "skills", Title: "Skills", Detail: "12 skills · 340 KB", Label: LabelSafe,
			Modes: share, Mode: ModeShare, Default: ModeShare, Bytes: 340 << 10,
			Note: "2 have the same name in work",
		},
		{ID: "agents", Title: "Agents", Detail: "5 agents · 48 KB", Modes: share, Mode: ModeShare, Default: ModeShare, Bytes: 48 << 10},
		{ID: "commands", Title: "Commands", Detail: "9 commands · 22 KB", Modes: share, Mode: ModeShare, Default: ModeShare, Bytes: 22 << 10},
		{ID: "claudemd", Title: "CLAUDE.md", Detail: "4.1 KB", Modes: share, Mode: ModeShare, Default: ModeShare, Bytes: 4200},
		{
			ID: "plugins", Title: "Plugins", Detail: "4 enabled", Modes: sameList, Mode: ModeSameList, Default: ModeSameList,
			Hint: "Each account installs its own copy of the same plugins.",
		},
		{ID: "settings", Title: "Settings", Detail: "settings.json · 2.3 KB", Label: LabelReview, Modes: copyOnly, Mode: ModeCopy, Default: ModeCopy, Bytes: 2400},
		{ID: "hooks", Title: "Hooks", Detail: "3 hooks", Label: LabelReview, Modes: copyOnly, Mode: ModeCopy, Default: ModeCopy, Bytes: 900},
		{
			ID: "mcp", Title: "MCP servers", Detail: "3 servers", Label: LabelCareful, Modes: copyOnly,
			Mode: ModeCopy, Default: ModeCopy, NeedsConfirm: true, Bytes: 1200,
			Hint: "May hold API keys. Turning it on asks twice.",
		},
		{ID: "history", Title: "Chat history", Detail: "1.2 GB", Label: LabelReview, Modes: copyOnly, Mode: ModeSkip, Default: ModeSkip, Bytes: 1288490188},
		{ID: "login", Title: "Login", Locked: true, LockedReason: "never cloned"},
		{ID: "account", Title: "Account info", Locked: true, LockedReason: "never cloned"},
	}
}

// legend is the text the screen puts under the list.
func legend() []string {
	return []string{
		"Share = one folder for both, add once and both see it.",
		"Copy = a separate copy now.",
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

// frame is the list with the legend and the summary under it, the way the
// screen will lay them out.
func frame(m Model, ctx uictx.Context, w int) string {
	return m.View(ctx) + "\n\n" + Legend(ctx, w, legend()...) + "\n\n" + pad(leadW) + SummaryLine(ctx, m.Summary())
}

// TestDemo prints the list at a few widths and in every tier. Run it with
// go test ./internal/ui/components/itemlist -run Demo -v to eyeball it.
func TestDemo(t *testing.T) {
	for _, tier := range icons.Tiers() {
		for _, w := range []int{60, 80, 120} {
			m := New(sample()).SetSize(w, 18)
			t.Logf("%s, %d columns:\n%s\n", tier, w, ansi.Strip(frame(m, ctxAt(w, 24, icons.For(tier)), w)))
		}
	}
}

func TestCarefulRowsStartOnSkip(t *testing.T) {
	m := New(sample())
	if got := m.Rows()[7].Mode; got != ModeSkip {
		t.Fatalf("MCP servers start on %s, want Skip until confirmed", got)
	}
	rows := sample()
	rows[7].Confirmed = true
	if got := New(rows).Rows()[7].Mode; got != ModeCopy {
		t.Fatalf("a confirmed careful row starts on %s, want its own mode", got)
	}
}

// The list never turns a guarded row on by itself: keys and clicks only
// report the wish, and Confirm is what does it.
func TestCarefulRowsOnlyReportTheWish(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := New(sample())
	m = m.SetCursor(7)
	for _, k := range []tea.KeyPressMsg{special(tea.KeyLeft), press(" ")} {
		next, cmd := m.Update(k)
		if next.Rows()[7].Mode != ModeSkip {
			t.Fatalf("%v turned the careful row on", k)
		}
		if cmd == nil {
			t.Fatalf("%v should report the wish", k)
		}
		want, ok := cmd().(WantsCarefulMsg)
		if !ok || want.ID != "mcp" || want.Mode != ModeCopy {
			t.Fatalf("%v produced %#v", k, cmd())
		}
	}

	// A click on its Copy option is the same wish.
	l := m.layout(ctx)
	y := m.mainLine(ctx, 7)
	x := l.ctrlX + l.slots[1].x + 1
	next, cmd := m.Click(ctx, x, y)
	if next.Rows()[7].Mode != ModeSkip || cmd == nil {
		t.Fatal("a click turned the careful row on, or reported nothing")
	}
	if _, ok := cmd().(WantsCarefulMsg); !ok {
		t.Fatalf("click produced %#v", cmd())
	}

	// The screen asked twice; now it is on.
	m, cmd = m.Confirm(7, ModeCopy)
	if m.Rows()[7].Mode != ModeCopy || cmd == nil {
		t.Fatal("Confirm did not turn the row on")
	}
	if c, ok := cmd().(ChangedMsg); !ok || c.Mode != ModeCopy {
		t.Fatalf("Confirm produced %#v", cmd())
	}
	// Off again is free, and the next time on is asked about afresh.
	m, _ = m.Update(special(tea.KeyRight))
	if m.Rows()[7].Mode != ModeSkip || m.Rows()[7].Confirmed {
		t.Fatal("turning the row off should clear the confirmation")
	}
	if _, cmd = m.Update(special(tea.KeyLeft)); cmd == nil {
		t.Fatal("no wish reported")
	} else if _, ok := cmd().(WantsCarefulMsg); !ok {
		t.Fatal("turning it on again should ask again")
	}
}

func TestLockedRowsNeverChange(t *testing.T) {
	m := New(sample()).SetCursor(9)
	for _, k := range []tea.KeyPressMsg{special(tea.KeyLeft), special(tea.KeyRight), press(" ")} {
		var cmd tea.Cmd
		m, cmd = m.Update(k)
		if cmd != nil {
			t.Fatalf("%v on a locked row produced %#v", k, cmd())
		}
	}
	if _, cmd := m.Confirm(9, ModeCopy); cmd != nil {
		t.Fatal("Confirm must refuse a locked row")
	}
	out := ansi.Strip(m.SetSize(100, 0).View(ctxAt(100, 30, icons.Unicode())))
	if !strings.Contains(out, "Locked: never cloned.") {
		t.Fatalf("a focused locked row should explain itself:\n%s", out)
	}
	if !strings.Contains(out, "locked    never cloned") && !strings.Contains(out, "locked   never cloned") {
		t.Fatalf("a locked row should say locked and why:\n%s", out)
	}
}

func TestArrowsAndSpaceChooseAModeAndReport(t *testing.T) {
	m := New(sample()) // Skills, on Share
	m, cmd := m.Update(special(tea.KeyRight))
	if m.Rows()[0].Mode != ModeCopy {
		t.Fatalf("→ moved Skills to %s", m.Rows()[0].Mode)
	}
	if c, ok := cmd().(ChangedMsg); !ok || c.ID != "skills" || c.Mode != ModeCopy || c.Index != 0 {
		t.Fatalf("→ produced %#v", cmd())
	}
	m, _ = m.Update(special(tea.KeyRight))
	if _, cmd = m.Update(special(tea.KeyRight)); cmd != nil {
		t.Fatal("→ at the last option should stop, not wrap")
	}
	m, _ = m.Update(press(" ")) // Space wraps
	if m.Rows()[0].Mode != ModeShare {
		t.Fatalf("space from Skip should wrap to Share, got %s", m.Rows()[0].Mode)
	}
	out := ansi.Strip(m.SetSize(100, 0).View(ctxAt(100, 30, icons.Unicode())))
	if strings.Contains(out, "suggested:") {
		t.Fatalf("a row back on its default should not suggest anything:\n%s", out)
	}
	m, _ = m.Update(special(tea.KeyRight))
	out = ansi.Strip(m.SetSize(100, 0).View(ctxAt(100, 30, icons.Unicode())))
	if !strings.Contains(out, "suggested: Share") {
		t.Fatalf("a row moved off its default should say what is suggested:\n%s", out)
	}
}

func TestCurrentModeReadsWithoutColour(t *testing.T) {
	out := ansi.Strip(New(sample()).SetSize(100, 0).View(ctxAt(100, 30, icons.Unicode())))
	for _, want := range []string{"[Share]      Copy   Skip", "[Same list]         Skip", "[Copy]  Skip", "Copy  [Skip]", "● Safe", "◆ Review", "▲ Careful"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestSummaryCountsModesAndCopyBytes(t *testing.T) {
	s := New(sample()).Summary()
	want := Summary{Share: 4, SameList: 1, Copy: 2, Skip: 2, Locked: 2, CopyBytes: 2400 + 900}
	if s != want {
		t.Fatalf("Summary = %+v, want %+v", s, want)
	}
	line := ansi.Strip(SummaryLine(ctxAt(80, 24, icons.Unicode()), s))
	if line != "Share 4 · Same list 1 · Copy 2 · Skip 2 · 3.2 KB to copy" {
		t.Fatalf("SummaryLine = %q", line)
	}
	if got := ansi.Strip(SummaryLine(ctxAt(80, 24, icons.ASCII()), Summary{})); got != "Skip 0" {
		t.Fatalf("an empty summary = %q", got)
	}
}

func TestLegendWrapsWithAHangingIndent(t *testing.T) {
	ctx := ctxAt(40, 24, icons.Unicode())
	out := ansi.Strip(Legend(ctx, 40, legend()...))
	ls := strings.Split(out, "\n")
	if len(ls) < 3 {
		t.Fatalf("a long entry should wrap at 40 columns:\n%s", out)
	}
	if !strings.HasPrefix(ls[0], "    Share = one folder") || !strings.HasPrefix(ls[1], "            ") {
		t.Fatalf("the wrapped part should hang under the text, not the term:\n%s", out)
	}
	checkWidth(t, out, 40)
	wide := ansi.Strip(Legend(ctxAt(100, 24, icons.Unicode()), 100, legend()...))
	if got := len(strings.Split(wide, "\n")); got != 2 {
		t.Fatalf("at 100 columns the legend should be two lines, got %d:\n%s", got, wide)
	}
}

// TestEveryWidthFits is the layout contract: at every width from 60 to 200,
// in every tier, no line is wider than the list, every title and every
// option is on screen.
func TestEveryWidthFits(t *testing.T) {
	for _, tier := range icons.Tiers() {
		for w := 60; w <= 200; w++ {
			ctx := ctxAt(w, 40, icons.For(tier))
			out := frame(New(sample()).SetSize(w, 0), ctx, w)
			checkWidth(t, out, w)
			plain := ansi.Strip(out)
			for _, want := range []string{"Skills", "MCP servers", "Account info", "[Share]", "[Same list]", "never cloned", "Careful"} {
				if !strings.Contains(plain, want) {
					t.Fatalf("%s at %d lost %q:\n%s", tier, w, want, plain)
				}
			}
		}
	}
}

func TestNarrowDropsSizeButKeepsItOnTheHint(t *testing.T) {
	out := ansi.Strip(New(sample()).SetSize(60, 0).View(ctxAt(60, 24, icons.Unicode())))
	if strings.Contains(out, "SIZE") {
		t.Fatalf("at 60 columns the size column should give way:\n%s", out)
	}
	if !strings.Contains(out, "12 skills · 340 KB") {
		t.Fatalf("the focused row should carry its size on the hint line:\n%s", out)
	}
}

func TestEmptyOneAndLong(t *testing.T) {
	ctx := ctxAt(60, 24, icons.ASCII())
	out := ansi.Strip(New(nil).SetSize(60, 6).View(ctx))
	if !strings.Contains(out, "Nothing to bring over.") {
		t.Fatalf("an empty list should say so:\n%s", out)
	}
	if _, cmd := New(nil).Update(special(tea.KeyEnter)); cmd != nil {
		t.Fatal("enter on an empty list should do nothing")
	}
	one := ansi.Strip(New(sample()[:1]).SetSize(60, 6).View(ctx))
	if !strings.Contains(one, "Skills") {
		t.Fatalf("a one-row list lost its row:\n%s", one)
	}
	long := strings.Repeat("very-long-", 20)
	rows := []Row{
		{ID: "a", Title: long, Detail: long, Modes: share, Mode: ModeShare, Note: long, Hint: long},
		{ID: "b", Title: "漢字の項目", Detail: "サイズ 12 KB", Modes: copyOnly, Mode: ModeCopy},
		{ID: "c", Title: "Locked", Locked: true, LockedReason: long},
	}
	for _, tier := range icons.Tiers() {
		for _, w := range []int{60, 80, 133, 200} {
			checkWidth(t, New(rows).SetSize(w, 0).View(ctxAt(w, 40, icons.For(tier))), w)
		}
	}
}

func TestScrollingKeepsTheFocusOnScreen(t *testing.T) {
	ctx := ctxAt(80, 24, icons.Unicode())
	m := New(sample()).SetSize(80, 6)
	for range len(sample()) {
		out := ansi.Strip(m.View(ctx))
		if n := len(lines(out)); n > 6 {
			t.Fatalf("a 6-line list drew %d lines:\n%s", n, out)
		}
		r, _ := m.Selected()
		if !strings.Contains(out, r.Title) {
			t.Fatalf("the focused row %s is off screen:\n%s", r.Title, out)
		}
		m, _ = m.Update(press("j"))
	}
	m, _ = m.Update(special(tea.KeyHome))
	if m.Cursor() != 0 {
		t.Fatal("home did not go to the first row")
	}
}

func TestEnterProceeds(t *testing.T) {
	_, cmd := New(sample()).Update(special(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	if _, ok := cmd().(ProceedMsg); !ok {
		t.Fatalf("enter produced %#v", cmd())
	}
}

// Line 0 is the heading; Skills (focused, with a note and no hint) takes
// lines 1-2, Agents is on line 3.
func TestRowAtAndModeAt(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := New(sample()).SetSize(100, 0)
	cases := []struct {
		y, want int
		ok      bool
	}{{0, 0, false}, {1, 0, true}, {2, 0, true}, {3, 1, true}, {4, 2, true}}
	for _, c := range cases {
		got, ok := m.RowAt(ctx, c.y)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("RowAt(%d) = %d,%v want %d,%v", c.y, got, ok, c.want, c.ok)
		}
	}
	l := m.layout(ctx)
	for _, c := range []struct {
		row, x int
		want   Mode
		ok     bool
	}{
		{0, l.ctrlX, ModeShare, true},
		{0, l.ctrlX + l.slots[1].x, ModeCopy, true},
		{0, l.ctrlX + l.slots[2].x + 2, ModeSkip, true},
		{0, l.ctrlX - 1, ModeSkip, false},
		{4, l.ctrlX + 3, ModeSameList, true},
		{4, l.ctrlX + l.slots[1].x, ModeSkip, false}, // plugins have no Copy
		{9, l.ctrlX, ModeSkip, false},                // locked
	} {
		got, ok := m.ModeAt(ctx, c.row, c.x)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ModeAt(%d, %d) = %s,%v want %s,%v", c.row, c.x, got, ok, c.want, c.ok)
		}
	}
}

func TestClickPicksAnOptionOrFocuses(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := New(sample()).SetSize(100, 0)
	l := m.layout(ctx)

	// A click on Agents' Copy focuses Agents and picks Copy.
	m, cmd := m.Click(ctx, l.ctrlX+l.slots[1].x+2, 3)
	if m.Cursor() != 1 || m.Rows()[1].Mode != ModeCopy || cmd == nil {
		t.Fatalf("click on an option: cursor=%d mode=%s", m.Cursor(), m.Rows()[1].Mode)
	}
	// A click on a title only focuses.
	m, cmd = m.Click(ctx, 6, 1)
	if m.Cursor() != 0 || cmd != nil {
		t.Fatalf("click on a title: cursor=%d cmd=%v", m.Cursor(), cmd)
	}
	// A click on the note line under Skills does not pick from the options
	// that happen to sit above it.
	before := m.Rows()[0].Mode
	m, _ = m.Click(ctx, l.ctrlX+l.slots[2].x+1, 2)
	if m.Rows()[0].Mode != before {
		t.Fatal("a click on a note line changed the mode")
	}
}

func TestPointerHoverAndWheel(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := New(sample()).SetSize(100, 0)
	const top = 2
	m, _, handled := m.Pointer(ctx, tea.MouseMotionMsg{X: 8, Y: ctx.BodyTop + top + 3}, top)
	if !handled || m.hover != 1 || m.Cursor() != 0 {
		t.Fatalf("hover=%d cursor=%d, want hover on Agents and the focus still", m.hover, m.Cursor())
	}
	if out := ansi.Strip(m.View(ctx)); !strings.Contains(out, "  ▸ Agents") {
		t.Errorf("the hovered row should carry a quiet caret:\n%s", out)
	}
	m, _, _ = m.Pointer(ctx, tea.MouseWheelMsg{Button: tea.MouseWheelDown}, top)
	if m.Cursor() != 1 {
		t.Fatalf("wheel moved the focus to %d", m.Cursor())
	}
	m, _, _ = m.Pointer(ctx, tea.MouseClickMsg{X: 8, Y: ctx.BodyTop + top + 1, Button: tea.MouseLeft}, top)
	if m.Cursor() != 0 {
		t.Fatalf("pointer click moved the focus to %d", m.Cursor())
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
				out := frame(New(sample()).SetSize(80, 18), ctx, 80)
				checkWidth(t, out, 80)
				if tier != icons.TierASCII {
					continue
				}
				for _, r := range ansi.Strip(out) {
					// The fixture's own text carries "·"; the tier must
					// not add anything else.
					if r > 0x7F && !strings.ContainsRune("●◆▲·", r) {
						t.Fatalf("the ascii tier drew %q", r)
					}
				}
			}
		}
	}
}

// TestGolden pins the list, its legend and its summary at the body sizes an
// 80×24 and a 100×30 terminal leave, in the unicode and ascii tiers, with
// colour stripped. The cursor is on MCP servers, so the golden shows a
// guarded row's hint.
func TestGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, size := range []struct{ w, h int }{{80, 24}, {100, 30}} {
		for _, tier := range []icons.Tier{icons.TierUnicode, icons.TierASCII} {
			name := fmt.Sprintf("itemlist_%dx%d_%s.golden", size.w, size.h, tier)
			t.Run(name, func(t *testing.T) {
				ctx := ctxAt(size.w, size.h, icons.For(tier))
				m := New(sample()).SetCursor(7).SetSize(size.w, ctx.BodyHeight-5)
				golden(t, name, ansi.Strip(frame(m, ctx, size.w)))
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
		t.Fatalf("reading %s: %v (run go test ./internal/ui/components/itemlist -update)", path, err)
	}
	if got != string(want) {
		t.Errorf("%s changed.\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}
