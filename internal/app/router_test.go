package app_test

import (
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/app"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// fakeScreen is the smallest thing that satisfies uictx.Screen.
type fakeScreen struct{ name string }

func (f fakeScreen) Init() tea.Cmd { return nil }
func (f fakeScreen) Update(tea.Msg, uictx.Context) (uictx.Screen, tea.Cmd) {
	return fakeScreen{name: f.name + "'"}, nil
}
func (f fakeScreen) View(uictx.Context) string { return f.name }
func (f fakeScreen) Title() string             { return f.name }
func (f fakeScreen) ShortHelp() []key.Binding  { return nil }
func (f fakeScreen) FullHelp() [][]key.Binding { return nil }

func topName(t *testing.T, r *app.Router) string {
	t.Helper()
	s, ok := r.Top()
	if !ok {
		t.Fatal("the router has no top screen")
	}
	return s.Title()
}

func TestRouterStartsWithRoot(t *testing.T) {
	r := app.NewRouter(fakeScreen{name: "home"})
	if r.Len() != 1 {
		t.Fatalf("Len = %d, want 1", r.Len())
	}
	if got := topName(t, r); got != "home" {
		t.Errorf("Top = %q, want home", got)
	}
}

func TestPushAndPop(t *testing.T) {
	r := app.NewRouter(fakeScreen{name: "home"})

	r.Push(fakeScreen{name: "clean"})
	r.Push(fakeScreen{name: "results"})
	if r.Len() != 3 {
		t.Fatalf("Len = %d, want 3", r.Len())
	}
	if got := topName(t, r); got != "results" {
		t.Errorf("Top = %q, want results", got)
	}

	if !r.Pop() {
		t.Error("Pop should have succeeded")
	}
	if got := topName(t, r); got != "clean" {
		t.Errorf("after Pop, Top = %q, want clean", got)
	}

	if !r.Pop() {
		t.Error("Pop should have succeeded")
	}
	if got := topName(t, r); got != "home" {
		t.Errorf("after two Pops, Top = %q, want home", got)
	}
}

// TestRootIsNeverPopped keeps Esc on the main menu from blanking the UI.
func TestRootIsNeverPopped(t *testing.T) {
	r := app.NewRouter(fakeScreen{name: "home"})
	if r.Pop() {
		t.Error("Pop on the root screen should report false")
	}
	if r.Len() != 1 {
		t.Errorf("Len = %d after popping the root, want 1", r.Len())
	}
	if got := topName(t, r); got != "home" {
		t.Errorf("Top = %q, want home", got)
	}
}

// TestPopRestoresTheScreenValue proves that screens are values: the state a
// screen had when it was covered comes back untouched.
func TestPopRestoresTheScreenValue(t *testing.T) {
	r := app.NewRouter(fakeScreen{name: "home"})
	r.Push(fakeScreen{name: "clean"})

	// Mutate the top the way the app does, through SetTop.
	top, _ := r.Top()
	next, _ := top.Update(nil, uictx.Context{})
	r.SetTop(next)
	if got := topName(t, r); got != "clean'" {
		t.Fatalf("SetTop did not store the updated screen: %q", got)
	}

	r.Pop()
	if got := topName(t, r); got != "home" {
		t.Errorf("Top = %q, want the untouched home", got)
	}
}

func TestReplaceKeepsDepth(t *testing.T) {
	r := app.NewRouter(fakeScreen{name: "firstrun"})
	r.Replace(fakeScreen{name: "home"})

	if r.Len() != 1 {
		t.Errorf("Len = %d after Replace, want 1", r.Len())
	}
	if got := topName(t, r); got != "home" {
		t.Errorf("Top = %q, want home", got)
	}
}

func TestResetCollapsesTheStack(t *testing.T) {
	r := app.NewRouter(fakeScreen{name: "firstrun"})
	r.Push(fakeScreen{name: "a"})
	r.Push(fakeScreen{name: "b"})

	r.Reset(fakeScreen{name: "home"})
	if r.Len() != 1 {
		t.Errorf("Len = %d after Reset, want 1", r.Len())
	}
	if got := topName(t, r); got != "home" {
		t.Errorf("Top = %q, want home", got)
	}
}

// TestNilScreensAreIgnored makes a mis-wired command unable to blank the UI.
func TestNilScreensAreIgnored(t *testing.T) {
	r := app.NewRouter(fakeScreen{name: "home"})
	r.Push(nil)
	r.Replace(nil)
	r.Reset(nil)
	r.SetTop(nil)

	if r.Len() != 1 {
		t.Errorf("Len = %d, want 1", r.Len())
	}
	if got := topName(t, r); got != "home" {
		t.Errorf("Top = %q, want home", got)
	}
}

// stoppableScreen counts the teardowns the router performs on it. It is a
// pointer type so the count survives being stored in the stack as an
// interface value.
type stoppableScreen struct {
	fakeScreen
	stopped int
}

func (s *stoppableScreen) Stop() { s.stopped++ }

func (s *stoppableScreen) Update(tea.Msg, uictx.Context) (uictx.Screen, tea.Cmd) { return s, nil }

// TestPopStopsTheScreen holds down the half of navigation that is easy to
// forget: a screen that is walked away from is torn down, so nothing it
// started keeps running behind the screen the user is now looking at.
func TestPopStopsTheScreen(t *testing.T) {
	root := fakeScreen{name: "home"}
	child := &stoppableScreen{fakeScreen: fakeScreen{name: "clean"}}

	r := app.NewRouter(root)
	r.Push(child)
	if !r.Pop() {
		t.Fatal("Pop should have succeeded")
	}
	if child.stopped != 1 {
		t.Errorf("Stop was called %d times on the popped screen, want 1", child.stopped)
	}

	// The root is never popped, so it is never stopped either.
	if r.Pop() {
		t.Error("Pop on the root screen should report false")
	}
}

// TestReplaceAndResetStopWhatTheyDiscard covers the other two ways a screen
// leaves the stack.
func TestReplaceAndResetStopWhatTheyDiscard(t *testing.T) {
	replaced := &stoppableScreen{fakeScreen: fakeScreen{name: "firstrun"}}
	r := app.NewRouter(replaced)
	r.Replace(fakeScreen{name: "home"})
	if replaced.stopped != 1 {
		t.Errorf("Replace stopped the old top %d times, want 1", replaced.stopped)
	}

	a := &stoppableScreen{fakeScreen: fakeScreen{name: "a"}}
	b := &stoppableScreen{fakeScreen: fakeScreen{name: "b"}}
	r.Push(a)
	r.Push(b)
	r.Reset(fakeScreen{name: "home"})
	if a.stopped != 1 || b.stopped != 1 {
		t.Errorf("Reset stopped a=%d b=%d times, want 1 each", a.stopped, b.stopped)
	}
	if r.Len() != 1 {
		t.Errorf("Len = %d after Reset, want 1", r.Len())
	}
}

func TestBreadcrumb(t *testing.T) {
	r := app.NewRouter(fakeScreen{name: ""})
	if r.Breadcrumb() != "" {
		t.Error("the root screen should show no breadcrumb")
	}
	r.Push(fakeScreen{name: "Settings"})
	if got := r.Breadcrumb(); got != "Settings" {
		t.Errorf("Breadcrumb = %q, want Settings", got)
	}
}
