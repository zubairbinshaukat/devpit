package clean

import (
	"os"
	"runtime"
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

// The folder browser runs as a Cmd whose result Bubble Tea delivers to this
// screen, not to the picker. If the screen drops it, the picker shows
// "Waiting for the folder browser…" forever, which is the bug a user hit.
func TestBrowseResultReachesThePicker(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the browse row is only enabled on Windows")
	}
	cfg := config.Default()
	cfg.FirstRunDone = true
	ctx := uictx.Context{Theme: theme.For(true), Icons: icons.Unicode(), Config: cfg, Width: 100, Height: 30, BodyHeight: 26}

	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	m := New().WithClock(func() time.Time { now = now.Add(time.Second); return now })
	m = m.openPicker(ctx)
	m.picker = m.picker.
		WithStat(func(string) (os.FileInfo, error) { return dirInfo{}, nil }).
		WithBrowse(func(string) (string, error) { return `D:\picked`, nil })

	// Down to the Browse row and activate it: the picker goes busy and hands
	// back the Cmd that will run the dialog.
	s, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown}, ctx)
	s, cmd := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, ctx)
	if cmd == nil {
		t.Fatal("activating Browse produced no command")
	}
	if out := ansi.Strip(s.View(ctx)); !strings.Contains(out, "Waiting for the folder browser") {
		t.Fatalf("Browse did not show the waiting line:\n%s", out)
	}

	// Run the Cmd and feed its result through the screen, as the runtime does.
	msg := cmd()
	if _, ok := msg.(pathpicker.BrowsedMsg); !ok {
		t.Fatalf("browse Cmd produced %T, want pathpicker.BrowsedMsg", msg)
	}
	s, cmd = s.Update(msg, ctx)
	if out := ansi.Strip(s.View(ctx)); strings.Contains(out, "Waiting for the folder browser") {
		t.Fatalf("the screen dropped the browse result; picker still waiting:\n%s", out)
	}
	if cmd == nil {
		t.Fatal("a picked folder produced no follow-up command")
	}
	if chosen, ok := cmd().(pathpicker.ChosenMsg); !ok || chosen.Path != `D:\picked` {
		t.Fatalf("follow-up was %T %+v, want ChosenMsg for the picked folder", cmd(), cmd())
	}
}

type dirInfo struct{ os.FileInfo }

func (dirInfo) IsDir() bool { return true }
