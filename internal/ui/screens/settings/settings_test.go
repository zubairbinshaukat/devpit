package settings

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/fonts"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/wt"
)

// testContext returns the render context every test in this package drives
// its screens with: a fixed 100x30 dark terminal, the unicode icon tier.
func testContext(cfg config.Config) uictx.Context {
	return uictx.Context{
		Theme:      theme.For(true),
		Icons:      icons.Unicode(),
		Config:     cfg,
		Width:      100,
		Height:     30,
		BodyHeight: 26,
	}
}

// pressKey builds the key message for a single printable rune, or the named
// special key ("enter", "esc").
func pressKey(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: 13}
	case "esc":
		return tea.KeyPressMsg{Code: 27}
	default:
		r := []rune(s)
		return tea.KeyPressMsg{Code: r[0], Text: s}
	}
}

// flattenCmd runs cmd and, if it produced a tea.BatchMsg, recursively runs
// every sub-command too, so a test can inspect every message a Batch
// produced without caring how many there were.
func flattenCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, flattenCmd(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// findConfigChanged returns the configuration carried by the first
// uictx.ConfigChangedMsg in msgs.
// findPush returns the first pushed screen among msgs.
func findPush(msgs []tea.Msg) (uictx.Screen, bool) {
	for _, m := range msgs {
		if p, ok := m.(uictx.PushScreenMsg); ok {
			return p.Screen, true
		}
	}
	return nil, false
}

// The probe is the only door into the nerd tier: Yes confirms the glyphs and
// leaves the tier on auto so the resolver picks nerd; No keeps unicode and,
// with the font installed, tells the user to reopen the terminal.
func TestProbeGatesTheNerdTier(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.FontInstalled = true
	ctx := testContext(cfg)

	s := newProbeScreen()
	_, cmd := s.Update(confirm.AnsweredMsg{ID: "probe", Answer: confirm.AnswerYes}, ctx)
	saved, ok := findConfigChanged(flattenCmd(cmd))
	if !ok || !saved.GlyphsConfirmed || saved.Icons != config.IconsAuto {
		t.Fatalf("Yes should confirm glyphs under auto: ok=%v cfg=%+v", ok, saved)
	}

	s = newProbeScreen()
	_, cmd = s.Update(confirm.AnsweredMsg{ID: "probe", Answer: confirm.AnswerNo}, ctx)
	msgs := flattenCmd(cmd)
	saved, ok = findConfigChanged(msgs)
	if !ok || saved.GlyphsConfirmed {
		t.Fatalf("No must not confirm glyphs: ok=%v cfg=%+v", ok, saved)
	}
	hinted := false
	for _, m := range msgs {
		if st, ok := m.(uictx.StatusMsg); ok && strings.Contains(st.Text, "Icon check") {
			hinted = true
		}
	}
	if !hinted {
		t.Error("No with the font installed should tell the user to reopen the terminal and re-run the check")
	}
}

func findConfigChanged(msgs []tea.Msg) (config.Config, bool) {
	for _, m := range msgs {
		if cc, ok := m.(uictx.ConfigChangedMsg); ok {
			return cc.Config, true
		}
	}
	return config.Config{}, false
}

// TestDevPortsParsingAndValidation covers ranges, duplicates across a range
// and a single port, and garbage input.
func TestDevPortsParsingAndValidation(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    []int
		wantErr bool
	}{
		{"single range", "3000-3005", []int{3000, 3001, 3002, 3003, 3004, 3005}, false},
		{"mixed singles and a range", "8080, 3000-3002, 8080", []int{3000, 3001, 3002, 8080}, false},
		{"dedupe across range and single", "3000-3002, 3001", []int{3000, 3001, 3002}, false},
		{"whitespace is trimmed", " 3000 , 3001 ", []int{3000, 3001}, false},
		{"garbage token", "abc", nil, true},
		{"garbage in a range", "abc-3000", nil, true},
		{"port zero", "0", nil, true},
		{"port too high", "65536", nil, true},
		{"reversed range", "10-5", nil, true},
		{"empty input", "", nil, true},
		{"only commas", " , , ", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseDevPorts(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseDevPorts(%q) = %v, want an error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDevPorts(%q) returned %v, want no error", c.in, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseDevPorts(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestDevPortsFormatRoundTrips checks that the default port list formats
// into readable ranges and parses straight back to the same list.
func TestDevPortsFormatRoundTrips(t *testing.T) {
	want := config.DefaultDevPorts()
	text := formatDevPorts(want)
	got, err := parseDevPorts(text)
	if err != nil {
		t.Fatalf("parseDevPorts(%q) failed: %v", text, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip = %v, want %v", got, want)
	}
}

// TestNeverTouchAddRemovePersists adds a path, then removes it, asserting
// each mutation's SaveConfig message carries the updated list.
func TestNeverTouchAddRemovePersists(t *testing.T) {
	cfg := config.Default()
	s := newNeverTouchScreen(cfg)
	ctx := testContext(cfg)

	next, _ := s.Update(pressKey("a"), ctx)
	s = next.(neverTouchScreen)
	if !s.adding {
		t.Fatal("expected adding mode after pressing a")
	}

	const path = `C:\Users\dev\never-touch-me`
	for _, r := range path {
		next, _ = s.Update(tea.KeyPressMsg{Code: r, Text: string(r)}, ctx)
		s = next.(neverTouchScreen)
	}

	next, cmd := s.Update(pressKey("enter"), ctx)
	s = next.(neverTouchScreen)
	if cmd == nil {
		t.Fatal("expected a save command after adding a path")
	}
	cfgAfterAdd, ok := findConfigChanged(flattenCmd(cmd))
	if !ok {
		t.Fatal("no ConfigChangedMsg after add")
	}
	if !reflect.DeepEqual(cfgAfterAdd.NeverTouch, []string{path}) {
		t.Fatalf("NeverTouch after add = %v, want [%s]", cfgAfterAdd.NeverTouch, path)
	}

	ctx = testContext(cfgAfterAdd)
	next, cmd = s.Update(pressKey("x"), ctx)
	s = next.(neverTouchScreen)
	_ = s
	if cmd == nil {
		t.Fatal("expected a save command after removing a path")
	}
	cfgAfterRemove, ok := findConfigChanged(flattenCmd(cmd))
	if !ok {
		t.Fatal("no ConfigChangedMsg after remove")
	}
	if len(cfgAfterRemove.NeverTouch) != 0 {
		t.Fatalf("NeverTouch after remove = %v, want empty", cfgAfterRemove.NeverTouch)
	}
}

// TestFolderMustExist checks that the default-folder editor refuses a path
// that does not exist and never saves a bad value.
func TestFolderMustExist(t *testing.T) {
	cfg := config.Default()
	s := newFolderScreen(cfg)
	ctx := testContext(cfg)

	next, _ := s.Update(pressKey("enter"), ctx) // edit the default row
	s = next.(folderScreen)
	if !s.editing {
		t.Fatal("expected editing mode after enter on the default row")
	}

	const bad = `Z:\this\path\does\not\exist\devpit-test`
	for _, r := range bad {
		next, _ = s.Update(tea.KeyPressMsg{Code: r, Text: string(r)}, ctx)
		s = next.(folderScreen)
	}

	next, cmd := s.Update(pressKey("enter"), ctx)
	s = next.(folderScreen)
	if cmd != nil {
		t.Fatal("submitting a folder that does not exist must not produce a save command")
	}
	if s.errMsg == "" {
		t.Fatal("expected a validation error")
	}
	if !s.editing {
		t.Fatal("a rejected submit should stay in editing mode")
	}

	// A real directory (the test's temp dir) must be accepted.
	good := t.TempDir()
	s.input.SetValue(good)
	next, cmd = s.Update(pressKey("enter"), ctx)
	s = next.(folderScreen)
	if cmd == nil {
		t.Fatal("expected a save command for a folder that exists")
	}
	saved, ok := findConfigChanged(flattenCmd(cmd))
	if !ok {
		t.Fatal("no ConfigChangedMsg for a valid folder")
	}
	if saved.DefaultProjectsFolder != good {
		t.Errorf("DefaultProjectsFolder = %q, want %q", saved.DefaultProjectsFolder, good)
	}
}

// TestRescanDefaultsToNo checks the safety rule that pressing Enter on a
// fresh rescan confirmation (its default answer) never clears the cache.
func TestRescanDefaultsToNo(t *testing.T) {
	cleared := false
	s := newRescanScreen(func() error { cleared = true; return nil })
	ctx := testContext(config.Default())

	if s.confirm.Answer() != confirm.AnswerNo {
		t.Fatal("rescan confirmation did not default to No")
	}

	next, cmd := s.Update(pressKey("enter"), ctx)
	if cmd == nil {
		t.Fatal("expected a command from the confirm dialog")
	}
	msg := cmd()
	rs := next.(rescanScreen)

	next2, cmd2 := rs.Update(msg, ctx)
	if cleared {
		t.Fatal("the scan cache was cleared without an explicit Yes")
	}
	if cmd2 == nil {
		t.Fatal("expected a Pop command on No")
	}
	if _, ok := cmd2().(uictx.PopScreenMsg); !ok {
		t.Error("expected the screen to pop back to the settings list on No")
	}
	_ = next2
}

// TestRescanYesClearsCache checks the other half: an explicit Yes does
// clear the cache and reports success.
func TestRescanYesClearsCache(t *testing.T) {
	cleared := false
	s := newRescanScreen(func() error { cleared = true; return nil })
	ctx := testContext(config.Default())

	next, cmd := s.Update(pressKey("y"), ctx)
	s = next.(rescanScreen)
	if cmd == nil {
		t.Fatal("expected a command from the confirm dialog")
	}
	msg := cmd()

	next, cmd = s.Update(msg, ctx)
	s = next.(rescanScreen)
	if !cleared {
		t.Fatal("expected the cache to be cleared after an explicit Yes")
	}
	if !s.done {
		t.Fatal("expected the screen to mark itself done")
	}
	if cmd == nil {
		t.Fatal("expected a status command")
	}
}

// TestFontInstallPatchesAllTerminalsAndFlipsConfig drives the font screen
// through a full install against two fake Windows Terminal locations, one
// of them unparsable, and checks that the unparsable one is skipped with a
// manual snippet while the config still ends up with the font on.
func TestFontInstallPatchesAllTerminalsAndFlipsConfig(t *testing.T) {
	const parsablePath = `C:\wt\stable\settings.json`
	const unparsablePath = `C:\wt\preview\settings.json`

	installed := false
	s := newFontScreen(
		func(fonts.Options) (fonts.State, error) {
			if installed {
				return fonts.State{Installed: true, FontPath: `C:\fonts\SymbolsNerdFontMono-Regular.ttf`}, nil
			}
			return fonts.State{Installed: false}, nil
		},
		func(context.Context, fonts.Options) (fonts.Result, error) {
			installed = true
			return fonts.Result{Installed: true, FontPath: `C:\fonts\SymbolsNerdFontMono-Regular.ttf`}, nil
		},
		func(fonts.Options) error { return nil },
		func(wt.FindOptions) []wt.Location {
			return []wt.Location{
				{Path: parsablePath, Flavor: wt.FlavorStable},
				{Path: unparsablePath, Flavor: wt.FlavorPreview},
			}
		},
		func(path, fallback string) (wt.PatchResult, error) {
			if path == unparsablePath {
				return wt.PatchResult{}, &wt.UnparsableError{Path: path, Err: errors.New("bad json")}
			}
			if fallback != wtFallbackFont {
				t.Fatalf("patch fallback = %q, want %q", fallback, wtFallbackFont)
			}
			return wt.PatchResult{Changed: true, BackupPath: path + ".devpit-bak"}, nil
		},
		func(string) error { return nil },
	)

	ctx := testContext(config.Default())
	next, cmd := s.Update(pressKey("enter"), ctx)
	fs := next.(fontScreen)
	if !fs.running {
		t.Fatal("expected running=true right after the trigger key")
	}
	if cmd == nil {
		t.Fatal("expected an install command")
	}

	msg := cmd()
	done, ok := msg.(fontDoneMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want fontDoneMsg", msg)
	}
	if !done.ok() {
		t.Fatalf("install reported failure: %+v", done)
	}
	if done.patched != 1 {
		t.Errorf("patched = %d, want 1", done.patched)
	}
	if !reflect.DeepEqual(done.manual, []string{unparsablePath}) {
		t.Errorf("manual = %v, want [%s]", done.manual, unparsablePath)
	}

	next2, cmd2 := fs.Update(done, ctx)
	fs2 := next2.(fontScreen)
	if fs2.running {
		t.Error("still running after the done message")
	}
	if !fs2.status.Installed {
		t.Error("status was not refreshed to installed")
	}
	if cmd2 == nil {
		t.Fatal("expected a save command")
	}
	saved, ok := findConfigChanged(flattenCmd(cmd2))
	if !ok {
		t.Fatal("no ConfigChangedMsg after a successful install")
	}
	if !saved.FontInstalled {
		t.Error("FontInstalled was not set")
	}
	if saved.Icons == config.IconsNerd || saved.GlyphsConfirmed {
		t.Errorf("an install must not turn nerd icons on before the probe: Icons=%q GlyphsConfirmed=%v", saved.Icons, saved.GlyphsConfirmed)
	}
	if _, ok := findPush(flattenCmd(cmd2)); !ok {
		t.Error("a successful install should push the glyph probe")
	}
}

// TestFontRemoveRestoresAndResetsConfig drives the font screen through a
// remove and checks every found Windows Terminal location is restored,
// the font is removed, and the config flips back to auto.
func TestFontRemoveRestoresAndResetsConfig(t *testing.T) {
	locations := []wt.Location{
		{Path: `C:\wt\stable\settings.json`, Flavor: wt.FlavorStable},
		{Path: `C:\wt\preview\settings.json`, Flavor: wt.FlavorPreview},
	}
	var restored []string
	removed := false

	s := newFontScreen(
		func(fonts.Options) (fonts.State, error) {
			return fonts.State{Installed: true, FontPath: `C:\fonts\SymbolsNerdFontMono-Regular.ttf`}, nil
		},
		func(context.Context, fonts.Options) (fonts.Result, error) { return fonts.Result{}, nil },
		func(fonts.Options) error { removed = true; return nil },
		func(wt.FindOptions) []wt.Location { return locations },
		func(string, string) (wt.PatchResult, error) { return wt.PatchResult{}, nil },
		func(path string) error { restored = append(restored, path); return nil },
	)

	ctx := testContext(config.Default())
	next, cmd := s.Update(pressKey("enter"), ctx)
	fs := next.(fontScreen)
	if !fs.running {
		t.Fatal("expected running=true right after the trigger key")
	}
	if cmd == nil {
		t.Fatal("expected a remove command")
	}

	msg := cmd()
	done, ok := msg.(fontDoneMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want fontDoneMsg", msg)
	}
	if !done.ok() {
		t.Fatalf("remove reported failure: %+v", done)
	}
	if !removed {
		t.Error("fonts.Remove was not called")
	}
	if len(restored) != len(locations) {
		t.Fatalf("restored %v, want one call per location %v", restored, locations)
	}

	_, cmd2 := fs.Update(done, ctx)
	if cmd2 == nil {
		t.Fatal("expected a save command")
	}
	saved, ok := findConfigChanged(flattenCmd(cmd2))
	if !ok {
		t.Fatal("no ConfigChangedMsg after a successful remove")
	}
	if saved.FontInstalled {
		t.Error("FontInstalled was not cleared")
	}
	if saved.Icons != config.IconsAuto {
		t.Errorf("Icons = %q, want %q", saved.Icons, config.IconsAuto)
	}
}

// TestFontInstallNonOfflineErrorRendersItsOwnText drives the font screen
// through an install failure that is neither *fonts.OfflineError nor
// *fonts.PolicyError (e.g. a checksum mismatch or a non-2xx HTTP status)
// and checks the screen shows that error's own text, not the fixed "No
// internet connection" message, plus a retry hint.
func TestFontInstallNonOfflineErrorRendersItsOwnText(t *testing.T) {
	wantErr := errors.New("fonts: checksum mismatch: got aaa, want bbb")

	s := newFontScreen(
		func(fonts.Options) (fonts.State, error) { return fonts.State{Installed: false}, nil },
		func(context.Context, fonts.Options) (fonts.Result, error) { return fonts.Result{}, wantErr },
		func(fonts.Options) error { return nil },
		func(wt.FindOptions) []wt.Location { return nil },
		func(string, string) (wt.PatchResult, error) { return wt.PatchResult{}, nil },
		func(string) error { return nil },
	)

	ctx := testContext(config.Default())
	next, cmd := s.Update(pressKey("enter"), ctx)
	fs := next.(fontScreen)
	if cmd == nil {
		t.Fatal("expected an install command")
	}

	msg := cmd()
	done, ok := msg.(fontDoneMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want fontDoneMsg", msg)
	}
	if done.offline {
		t.Fatal("a checksum mismatch must not be classified as offline")
	}
	if done.errText != wantErr.Error() {
		t.Fatalf("errText = %q, want %q", done.errText, wantErr.Error())
	}

	next2, _ := fs.Update(done, ctx)
	fs2 := next2.(fontScreen)
	if fs2.errMsg != wantErr.Error() {
		t.Fatalf("errMsg = %q, want the specific error %q, not the offline message", fs2.errMsg, wantErr.Error())
	}

	view := fs2.View(ctx)
	if !strings.Contains(view, wantErr.Error()) {
		t.Fatalf("view does not contain the specific error text: %q", view)
	}
	if strings.Contains(view, "No internet connection") {
		t.Fatalf("view wrongly shows the offline message for a non-offline error: %q", view)
	}
	if !strings.Contains(view, "try again") {
		t.Fatalf("view does not show a retry hint: %q", view)
	}
}

// TestManagerCycles checks the preferred-manager row cycles auto -> scoop
// -> winget -> choco -> auto in place, without pushing a sub-screen.
func TestManagerCycles(t *testing.T) {
	cfg := config.Default()
	want := []string{"scoop", "winget", "choco", ""}
	for _, w := range want {
		var changed bool
		cfg, changed = apply(cfg, rowManager)
		if !changed {
			t.Fatal("apply(rowManager) reported no change")
		}
		if cfg.PreferredManager != w {
			t.Errorf("PreferredManager = %q, want %q", cfg.PreferredManager, w)
		}
	}
}

// TestSpaceCyclesASetting is the regression test for the space key: Bubble
// Tea v2 names that key "space", so a binding written as " " never matches
// and the help line promising "enter/space" would be a lie.
func TestSpaceCyclesASetting(t *testing.T) {
	cfg := config.Default()
	cfg.Icons = config.IconsUnicode
	ctx := testContext(cfg)

	m := New()
	space := tea.KeyPressMsg{Code: ' ', Text: " "}
	if space.String() != "space" {
		t.Fatalf("the space key reports %q; this test is wired wrong", space.String())
	}
	if !key.Matches(space, m.toggle) {
		t.Fatal("the toggle binding does not match the space key")
	}

	next, cmd := m.Update(space, ctx)
	if _, ok := next.(Model); !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	saved, ok := findConfigChanged(flattenCmd(cmd))
	if !ok {
		t.Fatal("space on the icon row saved nothing")
	}
	if saved.Icons != config.IconsASCII {
		t.Errorf("Icons = %q after one space, want ascii", saved.Icons)
	}
}
