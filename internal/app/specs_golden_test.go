package app_test

import (
	"bytes"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/app"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/sysinfo"
	"github.com/zubairbinshaukat/devpit/internal/sysinfo/sysinfofake"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/home"
)

// driveSoon is drive for a screen whose reads may block on purpose: each
// command gets a moment to answer, and one that does not is left behind
// (the test's cleanup unblocks it), which is how a frame shows "reading".
func driveSoon(m tea.Model, msgs ...tea.Msg) tea.Model {
	queue := append([]tea.Msg(nil), msgs...)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		next, cmd := m.Update(msg)
		m = next
		queue = append(queue, runSoon(cmd)...)
	}
	return m
}

// runSoon is runCmd with a short wait per command.
func runSoon(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(150 * time.Millisecond):
		return nil
	}
	switch msg := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, runSoon(c)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

// specsAt opens Settings › PC specs over src and presses keys there.
func specsAt(t *testing.T, cfg config.Config, src sysinfofake.Source, w, h int, keys ...tea.Msg) tea.Model {
	t.Helper()
	o := goldenOptions(cfg)
	o.ScreenFactory[home.SectionSettings] = settingsWithSpecs(skillAs(service.AgentCreate, service.AgentCreate), fakeSpecs(src))
	m := tea.Model(app.New(o))
	m = drive(m, tea.WindowSizeMsg{Width: w, Height: h}, tea.BackgroundColorMsg{Color: nil})
	m = walkTo(t, m, home.SectionSettings, "")
	if slices.Index(settingsOrder, "PC specs") != 0 {
		t.Fatal("PC specs should be the first row of Settings")
	}
	m = driveSoon(m, press("enter"))
	for _, k := range keys {
		m = driveSoon(m, k)
	}
	return m
}

// blocked is a source whose listed sections never answer during the test.
func blocked(t *testing.T, r sysinfo.Report, secs ...sysinfo.Section) sysinfofake.Source {
	src := sysinfofake.New(r)
	src.Block = map[sysinfo.Section]chan struct{}{}
	for _, s := range secs {
		ch := make(chan struct{})
		src.Block[s] = ch
		t.Cleanup(func() { close(ch) })
	}
	return src
}

// TestSpecsGoldens captures Settings › PC specs in each state, at both sizes
// and in the unicode and ascii tiers, with NO_COLOR.
func TestSpecsGoldens(t *testing.T) {
	keyEnd := tea.KeyPressMsg{Code: tea.KeyEnd}
	for _, c := range []struct {
		name     string
		src      func(t *testing.T) sysinfofake.Source
		keys     []tea.Msg
		sentinel string
	}{
		{"specs_reading", func(t *testing.T) sysinfofake.Source {
			return blocked(t, sysinfofake.Good(), sysinfo.Sections()...)
		}, nil, "reading"},
		{"specs_partial", func(t *testing.T) sysinfofake.Source {
			return blocked(t, sysinfofake.Good(), sysinfo.SecDisks, sysinfo.SecWindows)
		}, nil, "Battery"},
		{"specs_good", func(*testing.T) sysinfofake.Source { return sysinfofake.New(sysinfofake.Good()) }, nil, "Nothing stands out"},
		{"specs_flags", func(*testing.T) sysinfofake.Source { return sysinfofake.New(sysinfofake.Flags()) }, nil, "very worn"},
		{"specs_desktop", func(*testing.T) sysinfofake.Source { return sysinfofake.New(sysinfofake.Desktop()) }, nil, "Fabrikam"},
		{"specs_missing", func(*testing.T) sysinfofake.Source {
			s := sysinfofake.New(sysinfofake.Missing())
			s.Fail = map[sysinfo.Section]error{sysinfo.SecDisks: sysinfofake.ErrBlocked}
			return s
		}, nil, "This PC"},
		{"specs_soldered", func(*testing.T) sysinfofake.Source { return sysinfofake.New(sysinfofake.Soldered()) }, []tea.Msg{press("right"), press("right")}, "soldered"},
		{"specs_bottom", func(*testing.T) sysinfofake.Source { return sysinfofake.New(sysinfofake.Flags()) }, []tea.Msg{keyEnd}, "End of the page"},
		{"specs_export_ask", func(*testing.T) sysinfofake.Source { return sysinfofake.New(sysinfofake.Good()) }, []tea.Msg{press("c")}, "Include serial numbers and MAC addresses?"},
		{"specs_elevate_declined", func(*testing.T) sysinfofake.Source { return sysinfofake.New(sysinfofake.Good()) }, []tea.Msg{press("e"), press("right"), press("right"), press("right")}, "Skipped: needs admin"},
	} {
		eachFrame(t, c.name, func(t *testing.T, cfg config.Config, w, h int) []byte {
			m := specsAt(t, cfg, c.src(t), w, h, c.keys...)
			got := []byte(ansi.Strip(m.View().Content))
			if !bytes.Contains(got, []byte(c.sentinel)) {
				t.Fatalf("%q is not on screen:\n%s", c.sentinel, got)
			}
			return got
		})
	}
}
