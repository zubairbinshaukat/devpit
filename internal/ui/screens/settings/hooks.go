package settings

import (
	"context"

	"github.com/zubairbinshaukat/devpit/internal/fonts"
	"github.com/zubairbinshaukat/devpit/internal/wt"
)

// Hooks are the engine calls the icon font and rescan screens make. Every
// field is optional: a nil one keeps the real function. They exist so a
// caller that must not touch fonts, Windows Terminal or the scan cache, such
// as the documentation screenshot renderer in internal/shots, can draw those
// screens from demo data.
type Hooks struct {
	// FontStatus reads whether the icon font is installed.
	FontStatus func(fonts.Options) (fonts.State, error)
	// FontInstall installs the icon font.
	FontInstall func(context.Context, fonts.Options) (fonts.Result, error)
	// FontRemove removes it.
	FontRemove func(fonts.Options) error
	// FindTerminals lists Windows Terminal settings files.
	FindTerminals func(wt.FindOptions) []wt.Location
	// PatchTerminal points one settings file at the font.
	PatchTerminal func(path, fallback string) (wt.PatchResult, error)
	// RestoreTerminal undoes a patch.
	RestoreTerminal func(path string) error
	// ClearCache deletes the scan cache.
	ClearCache func() error
}

// WithHooks replaces the engine calls the hooks name and keeps the rest.
func (m Model) WithHooks(h Hooks) Model {
	if h.FontStatus != nil {
		m.fontStatusFn = h.FontStatus
	}
	if h.FontInstall != nil {
		m.fontInstallFn = h.FontInstall
	}
	if h.FontRemove != nil {
		m.fontRemoveFn = h.FontRemove
	}
	if h.FindTerminals != nil {
		m.wtFindFn = h.FindTerminals
	}
	if h.PatchTerminal != nil {
		m.wtPatchFn = h.PatchTerminal
	}
	if h.RestoreTerminal != nil {
		m.wtRestoreFn = h.RestoreTerminal
	}
	if h.ClearCache != nil {
		m.clearCacheFn = h.ClearCache
	}
	return m
}
