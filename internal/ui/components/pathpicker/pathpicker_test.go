package pathpicker_test

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/pathpicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// fakeInfo is the os.FileInfo a fake stat returns.
type fakeInfo struct {
	name string
	dir  bool
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return 0 }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.dir }
func (f fakeInfo) Sys() any           { return nil }

// statTable builds a fake stat over a map of path to "is a directory".
func statTable(dirs map[string]bool) func(string) (os.FileInfo, error) {
	return func(p string) (os.FileInfo, error) {
		isDir, ok := dirs[strings.ToLower(p)]
		if !ok {
			return nil, os.ErrNotExist
		}
		return fakeInfo{name: p, dir: isDir}, nil
	}
}

func testContext() uictx.Context {
	return uictx.Context{
		Theme:      theme.For(true),
		Icons:      icons.Unicode(),
		Config:     config.Default(),
		Width:      100,
		Height:     30,
		BodyHeight: 26,
	}
}

func cfgWith(def string, recent ...string) config.Config {
	c := config.Default()
	c.DefaultProjectsFolder = def
	c.RecentFolders = recent
	return c
}

func picker(dirs map[string]bool, cfg config.Config) pathpicker.Model {
	return pathpicker.New(cfg).
		WithStat(statTable(dirs)).
		WithBrowse(func(string) (string, error) { return "", nil })
}

func press(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: []rune(s)[0], Text: s} }

func typeIn(m pathpicker.Model, s string) pathpicker.Model {
	for _, r := range s {
		m, _ = m.Update(press(string(r)))
	}
	return m
}

func TestChoosingARecentFolderEmitsIt(t *testing.T) {
	m := picker(map[string]bool{`d:\work`: true}, cfgWith(`D:\work`))
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on the default folder produced no command")
	}
	msg, ok := cmd().(pathpicker.ChosenMsg)
	if !ok {
		t.Fatalf("enter produced %T, want ChosenMsg", cmd())
	}
	if msg.Path != `D:\work` {
		t.Errorf("ChosenMsg carried %q", msg.Path)
	}
}

func TestValidationRefusesTheThreeBadAnswers(t *testing.T) {
	dirs := map[string]bool{
		`d:\work`:           true,
		`d:\work\notes.txt`: false,
	}
	cases := []struct {
		name  string
		typed string
		want  error
	}{
		{"empty", "   ", pathpicker.ErrEmptyPath},
		{"missing", `D:\nope`, pathpicker.ErrNoSuchFolder},
		{"a file", `D:\work\notes.txt`, pathpicker.ErrNotADirectory},
		{"unc", `\\server\share`, pathpicker.ErrNetworkPath},
		{"unc forward slashes", `//server/share`, pathpicker.ErrNetworkPath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := picker(dirs, cfgWith(""))
			// Move to the "Type a path…" row and open it.
			m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if !m.Typing() {
				t.Fatal("the first row of an empty config is not the typing row")
			}
			m = typeIn(m, tc.typed)
			next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd != nil {
				t.Fatalf("%q was accepted: %T", tc.typed, cmd())
			}
			if !errors.Is(next.Err(), tc.want) {
				t.Fatalf("%q reported %v, want %v", tc.typed, next.Err(), tc.want)
			}
			if out := ansi.Strip(next.View(testContext())); !strings.Contains(out, tc.want.Error()) {
				t.Errorf("the refusal is not on screen:\n%s", out)
			}
		})
	}
}

func TestTypingAValidPathEmitsIt(t *testing.T) {
	m := picker(map[string]bool{`d:\code`: true}, cfgWith(""))
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = typeIn(m, `D:\code`)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("a valid typed path produced no command")
	}
	msg, ok := cmd().(pathpicker.ChosenMsg)
	if !ok {
		t.Fatalf("produced %T, want ChosenMsg", cmd())
	}
	if msg.Path != `D:\code` {
		t.Errorf("ChosenMsg carried %q", msg.Path)
	}
}

func TestBrowseResultIsValidatedLikeAnythingElse(t *testing.T) {
	m := pathpicker.New(cfgWith("")).
		WithStat(statTable(map[string]bool{`d:\picked`: true})).
		WithBrowse(func(string) (string, error) { return `\\server\share`, nil })

	// Walk to the Browse row: type, then browse.
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Skip("the browse row is disabled on this platform")
	}
	after, cmd2 := next.Update(cmd())
	if cmd2 != nil {
		t.Fatalf("a UNC path from the browser was accepted: %T", cmd2())
	}
	if !errors.Is(after.Err(), pathpicker.ErrNetworkPath) {
		t.Errorf("the browser's UNC path reported %v", after.Err())
	}
}

// activateBrowseRow walks a fresh picker to the Browse row and activates it,
// returning the model mid-browse (busy) and the tea.Cmd the activation
// produced. It skips the test when Browse is disabled, which is how this
// helper behaves the same on every platform pathpicker_test.go runs on.
func activateBrowseRow(t *testing.T, m pathpicker.Model) (pathpicker.Model, tea.Cmd) {
	t.Helper()
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Skip("the browse row is disabled on this platform")
	}
	return next, cmd
}

// TestBrowseShowsWaitingWhileBusy is the state the bug report described: the
// picker must show the waiting line the instant Browse is activated, and
// [pathpicker.Model.Update] must be the thing a screen feeds the browse
// Cmd's result back into, or that line never goes away.
func TestBrowseShowsWaitingWhileBusy(t *testing.T) {
	m := pathpicker.New(cfgWith(""))
	waiting, _ := activateBrowseRow(t, m)
	if out := ansi.Strip(waiting.View(testContext())); !strings.Contains(out, "Waiting for the folder browser") {
		t.Fatalf("activating Browse did not show the waiting line:\n%s", out)
	}
}

// TestBrowseCancelClearsBusyWithoutAnError is what a Cancel in the real
// dialog produces: an empty path and a nil error. The picker must return to
// the list with no error and no longer show "waiting", which only happens if
// whatever owns the picker routes the browse Cmd's result — a BrowsedMsg —
// back into Update.
func TestBrowseCancelClearsBusyWithoutAnError(t *testing.T) {
	m := pathpicker.New(cfgWith("")).
		WithBrowse(func(string) (string, error) { return "", nil })
	waiting, cmd := activateBrowseRow(t, m)

	after, _ := waiting.Update(cmd())
	out := ansi.Strip(after.View(testContext()))
	if strings.Contains(out, "Waiting for the folder browser") {
		t.Errorf("a cancelled browse left the picker waiting:\n%s", out)
	}
	if after.Err() != nil {
		t.Errorf("a cancelled browse reported an error: %v", after.Err())
	}
}

// TestBrowseErrorClearsBusyAndIsShown is the failure path: the platform
// browse function returns an error (a launch failure, a timeout, anything),
// and that must clear busy and put the error on screen rather than leaving
// the picker stuck.
func TestBrowseErrorClearsBusyAndIsShown(t *testing.T) {
	boom := errors.New("the folder browser did not respond — type the path instead")
	m := pathpicker.New(cfgWith("")).
		WithBrowse(func(string) (string, error) { return "", boom })
	waiting, cmd := activateBrowseRow(t, m)

	after, _ := waiting.Update(cmd())
	out := ansi.Strip(after.View(testContext()))
	if strings.Contains(out, "Waiting for the folder browser") {
		t.Errorf("a failed browse left the picker waiting:\n%s", out)
	}
	if !errors.Is(after.Err(), boom) {
		t.Errorf("after.Err() = %v, want %v", after.Err(), boom)
	}
	if !strings.Contains(out, boom.Error()) {
		t.Errorf("the browse error is not on screen:\n%s", out)
	}
}

// TestBrowseSuccessEmitsChosen is the happy path all the way through: a
// picked path clears busy and produces a ChosenMsg, the same as typing it.
func TestBrowseSuccessEmitsChosen(t *testing.T) {
	m := pathpicker.New(cfgWith("")).
		WithStat(statTable(map[string]bool{`d:\picked`: true})).
		WithBrowse(func(string) (string, error) { return `D:\picked`, nil })
	waiting, cmd := activateBrowseRow(t, m)

	after, cmd2 := waiting.Update(cmd())
	if cmd2 == nil {
		t.Fatal("a valid browsed path produced no command")
	}
	msg, ok := cmd2().(pathpicker.ChosenMsg)
	if !ok {
		t.Fatalf("produced %T, want ChosenMsg", cmd2())
	}
	if msg.Path != `D:\picked` {
		t.Errorf("ChosenMsg carried %q", msg.Path)
	}
	if out := ansi.Strip(after.View(testContext())); strings.Contains(out, "Waiting for the folder browser") {
		t.Errorf("a successful browse left the picker waiting:\n%s", out)
	}
}

func TestEscCancels(t *testing.T) {
	m := picker(map[string]bool{`d:\work`: true}, cfgWith(`D:\work`))
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("esc produced no command")
	}
	if _, ok := cmd().(pathpicker.CancelledMsg); !ok {
		t.Fatalf("esc produced %T, want CancelledMsg", cmd())
	}
}

func TestIsUNC(t *testing.T) {
	for path, want := range map[string]bool{
		`\\server\share`: true,
		`//server/share`: true,
		`\\?\D:\work`:    true,
		`D:\work`:        false,
		`work`:           false,
		``:               false,
	} {
		if got := pathpicker.IsUNC(path); got != want {
			t.Errorf("IsUNC(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestRecentFoldersAreListedWithoutDuplicates(t *testing.T) {
	m := picker(map[string]bool{`d:\work`: true}, cfgWith(`D:\work`, `D:\work`, `D:\side`))
	out := ansi.Strip(m.View(testContext()))
	if strings.Count(out, `D:\work`) != 1 {
		t.Errorf("the default folder is listed twice:\n%s", out)
	}
	if !strings.Contains(out, `D:\side`) {
		t.Errorf("a recent folder is missing:\n%s", out)
	}
}
