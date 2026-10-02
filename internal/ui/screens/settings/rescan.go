package settings

import (
	"errors"
	"os"
	"path/filepath"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/scan"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// clearCacheFunc deletes the scan cache. Production points it at
// clearScanCache; tests substitute a fake so no test ever touches a real
// file.
type clearCacheFunc func() error

// rescanScreen confirms (default No, per the safety rule every dialog in
// Devpit follows) before clearing the scan cache.
type rescanScreen struct {
	confirm confirm.Model
	clearFn clearCacheFunc
	done    bool
	errMsg  string
}

// newRescanScreen returns the rescan confirmation sub-screen.
func newRescanScreen(clearFn clearCacheFunc) rescanScreen {
	return rescanScreen{
		confirm: confirm.New("rescan", "Forget the last scan?",
			"The next scan reads the disk from scratch instead of showing last time's results first. Nothing on disk is deleted."),
		clearFn: clearFn,
	}
}

// Init implements uictx.Screen.
func (s rescanScreen) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (s rescanScreen) Title() string { return "Forget last scan" }

// ShortHelp implements uictx.Screen.
func (s rescanScreen) ShortHelp() []key.Binding { return s.confirm.Keys.ShortHelp() }

// FullHelp implements uictx.Screen.
func (s rescanScreen) FullHelp() [][]key.Binding { return s.confirm.Keys.FullHelp() }

// Update implements uictx.Screen.
func (s rescanScreen) Update(msg tea.Msg, _ uictx.Context) (uictx.Screen, tea.Cmd) {
	if s.done {
		return s, nil
	}
	if ans, ok := msg.(confirm.AnsweredMsg); ok {
		if ans.Answer != confirm.AnswerYes {
			return s, uictx.Pop()
		}
		s.done = true
		if err := s.clearFn(); err != nil {
			s.errMsg = err.Error()
			return s, uictx.Status("danger", "Could not forget the last scan: "+err.Error())
		}
		return s, uictx.Status("success", "Last scan forgotten")
	}

	next, cmd := s.confirm.Update(msg)
	s.confirm = next
	return s, cmd
}

// View implements uictx.Screen.
func (s rescanScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	if s.done {
		if s.errMsg != "" {
			return th.Danger.Render(ctx.Icons.Fail + " " + s.errMsg)
		}
		return th.Success.Render(ctx.Icons.Tick + " Done: the next scan starts fresh. Esc to go back.")
	}
	return s.confirm.View(ctx)
}

// clearScanCache deletes <CacheDir>/scan.gob and any scan.gob.broken-*
// quarantine files scan.LoadCache/SaveCache may have left behind. A missing
// file is not an error: there was simply nothing to clear.
func clearScanCache() error {
	dir, err := config.CacheDir()
	if err != nil {
		return err
	}
	if rmErr := removeIfExists(filepath.Join(dir, scan.CacheFileName)); rmErr != nil {
		return rmErr
	}
	broken, err := filepath.Glob(filepath.Join(dir, scan.CacheFileName+".broken-*"))
	if err != nil {
		return err
	}
	for _, path := range broken {
		if err := removeIfExists(path); err != nil {
			return err
		}
	}
	return nil
}

// removeIfExists deletes path, treating "already gone" as success.
func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
