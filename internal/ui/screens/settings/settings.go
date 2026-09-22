// Package settings is the preferences screen.
//
// Four settings cycle in place — icon tier, theme, emoji, telemetry and the
// preferred package manager — the same way this screen always has: Enter or
// Space moves the value to its next choice and saves immediately, so there
// is no separate save step to forget.
//
// Everything else (folders, the never-touch list, dev ports, the active/
// older day windows, the icon font and the tool rescan) needs more than one
// value at a time, so Enter on those rows pushes a small private sub-screen
// instead. Esc is handled globally by the router (see internal/app), so
// popping a sub-screen without having pressed its save key already discards
// whatever was half-typed: nothing in this package persists a value except
// through an explicit uictx.SaveConfig call.
package settings

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/fonts"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/wt"
)

// Row identifiers.
const (
	rowIcons      = "icons"
	rowTheme      = "theme"
	rowEmoji      = "emoji"
	rowTelemetry  = "telemetry"
	rowFolders    = "folders"
	rowManager    = "manager"
	rowNeverT     = "never_touch"
	rowDevPorts   = "dev_ports"
	rowActiveDays = "active_days"
	rowOlderDays  = "older_days"
	rowFont       = "font"
	rowProbe      = "probe"
	rowRescan     = "rescan"
)

// iconTiers, themes and managers are the cycle orders for the in-place enum
// settings. managers starts with "", which Preferred (internal/tools/
// managers) already treats as "auto": try Scoop, then winget, then
// Chocolatey.
var (
	iconTiers = []string{config.IconsAuto, config.IconsNerd, config.IconsUnicode, config.IconsASCII}
	themes    = config.Themes
	managers  = []string{"", "scoop", "winget", "choco"}
)

// Model is the settings screen.
type Model struct {
	menu   menu.Model
	toggle key.Binding
	back   key.Binding

	// Engine hooks. Production leaves these at their New defaults, which
	// call the real internal/fonts and internal/wt packages plus the real
	// scan cache directory; tests overwrite them directly (this package can
	// reach its own unexported fields) so no test ever downloads a font,
	// patches a real Windows Terminal settings file, or deletes a real
	// cache.
	fontStatusFn  fontStatusFunc
	fontInstallFn fontInstallFunc
	fontRemoveFn  fontRemoveFunc
	wtFindFn      wtFindFunc
	wtPatchFn     wtPatchFunc
	wtRestoreFn   wtRestoreFunc
	clearCacheFn  clearCacheFunc
}

// New returns the settings screen.
func New() Model {
	return Model{
		menu:   menu.New(nil).DescOnSelectedOnly(true),
		toggle: key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter/space", "change")),
		back:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),

		fontStatusFn:  fonts.Status,
		fontInstallFn: fonts.Install,
		fontRemoveFn:  fonts.Remove,
		wtFindFn:      wt.FindSettings,
		wtPatchFn:     wt.Patch,
		wtRestoreFn:   wt.Restore,
		clearCacheFn:  clearScanCache,
	}
}

// Init implements uictx.Screen.
func (m Model) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (m Model) Title() string { return "Settings" }

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	return []key.Binding{m.menu.Keys.Up, m.toggle, m.back}
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{m.menu.Keys.Up, m.menu.Keys.Down},
		{m.toggle, m.back},
	}
}

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	m.menu = m.menu.SetItems(rows(ctx.Config))

	if km, ok := msg.(tea.KeyPressMsg); ok && key.Matches(km, m.toggle) {
		it, ok := m.menu.Selected()
		if !ok || it.Disabled {
			return m, nil
		}
		if scr, ok := m.subScreen(it.ID, ctx.Config); ok {
			return m, uictx.Push(scr)
		}
		cfg, changed := apply(ctx.Config, it.ID)
		if !changed {
			return m, nil
		}
		return m, tea.Batch(uictx.SaveConfig(cfg), uictx.Status("success", "Saved"))
	}

	next, cmd := m.menu.Update(msg)
	m.menu = next
	return m, cmd
}

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string {
	mm := m.menu.SetItems(rows(ctx.Config))
	var b strings.Builder
	b.WriteString(ctx.Theme.Muted.Render("Changes save as soon as you make them."))
	b.WriteString("\n\n")
	b.WriteString(mm.View(ctx))
	return b.String()
}

// subScreen builds the private sub-screen a row pushes, if it has one. Rows
// that just cycle a value in place (icons, theme, emoji, telemetry,
// manager) have none and fall through to apply instead.
func (m Model) subScreen(id string, cfg config.Config) (uictx.Screen, bool) {
	switch id {
	case rowFolders:
		return newFolderScreen(cfg), true
	case rowNeverT:
		return newNeverTouchScreen(cfg), true
	case rowDevPorts:
		return newDevPortsScreen(cfg), true
	case rowActiveDays:
		return newNumberScreen(numberField{
			title: "Active project window",
			hint:  "Projects touched within this many days count as active and are never pre-ticked for cleanup.",
			value: cfg.ActiveDays,
			apply: func(c config.Config, n int) config.Config { c.ActiveDays = n; return c },
		}), true
	case rowOlderDays:
		return newNumberScreen(numberField{
			title: "Older-than filter",
			hint:  `The results screen's "older than" filter uses this many days.`,
			value: cfg.OlderDays,
			apply: func(c config.Config, n int) config.Config { c.OlderDays = n; return c },
		}), true
	case rowFont:
		return newFontScreen(m.fontStatusFn, m.fontInstallFn, m.fontRemoveFn, m.wtFindFn, m.wtPatchFn, m.wtRestoreFn), true
	case rowProbe:
		return newProbeScreen(), true
	case rowRescan:
		return newRescanScreen(m.clearCacheFn), true
	default:
		return nil, false
	}
}

// rows builds the settings list from the current configuration, so the screen
// always shows real values rather than a snapshot taken when it was pushed.
func rows(cfg config.Config) []menu.Item {
	return []menu.Item{
		{ID: rowIcons, Title: "Icons: " + cfg.Icons, Desc: "auto, nerd, unicode or ascii"},
		{ID: rowTheme, Title: "Theme: " + cfg.Theme, Desc: "auto follows your terminal; aqua, blue, rose and mono change the accent"},
		{ID: rowEmoji, Title: "Emoji: " + onOff(cfg.Emoji), Desc: "emoji in headers and summaries only, never in tables"},
		{ID: rowTelemetry, Title: "Usage stats: " + onOff(cfg.TelemetryOptIn), Desc: "totals only, never paths or names. Off unless you turn it on"},
		{ID: rowFolders, Title: "Projects folder: " + folderSummary(cfg), Desc: "Default scan folder and your recent folders"},
		{ID: rowManager, Title: "Preferred package manager: " + managerLabel(cfg.PreferredManager), Desc: "auto picks Scoop, then winget, then Chocolatey"},
		{ID: rowNeverT, Title: fmt.Sprintf("Never-touch list: %d path(s)", len(cfg.NeverTouch)), Desc: "Folders Devpit will never scan or delete"},
		{ID: rowDevPorts, Title: fmt.Sprintf("Dev ports: %d configured", len(cfg.DevPorts)), Desc: "Ports the busy-ports view checks"},
		{ID: rowActiveDays, Title: fmt.Sprintf("Active project window: %d days", cfg.ActiveDays), Desc: "Projects touched this recently are never pre-ticked"},
		{ID: rowOlderDays, Title: fmt.Sprintf("Older-than filter: %d days", cfg.OlderDays), Desc: "Used by the results screen's age filter"},
		{ID: rowFont, Title: "Icon font: " + fontLabel(cfg), Desc: "Installs Symbols Nerd Font Mono and patches Windows Terminal"},
		{ID: rowProbe, Title: "Icon check: " + probeLabel(cfg), Desc: "Checks whether Nerd Font glyphs render here before using them"},
		{ID: rowRescan, Title: "Rescan my tools", Desc: "Clears the scan cache so the next scan reads the disk fresh"},
	}
}

// apply cycles one in-place setting and reports whether anything changed.
func apply(cfg config.Config, id string) (config.Config, bool) {
	switch id {
	case rowIcons:
		cfg.Icons = cycle(iconTiers, cfg.Icons)
	case rowTheme:
		cfg.Theme = cycle(themes, cfg.Theme)
	case rowEmoji:
		cfg.Emoji = !cfg.Emoji
	case rowTelemetry:
		cfg.TelemetryOptIn = !cfg.TelemetryOptIn
	case rowManager:
		cfg.PreferredManager = cycle(managers, cfg.PreferredManager)
	default:
		return cfg, false
	}
	return cfg, true
}

// cycle returns the value after current in values, wrapping around.
func cycle(values []string, current string) string {
	for i, v := range values {
		if v == current {
			return values[(i+1)%len(values)]
		}
	}
	return values[0]
}

// onOff renders a boolean the way the UI words it.
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// folderSummary is the one-line value shown on the folders row.
func folderSummary(cfg config.Config) string {
	if cfg.DefaultProjectsFolder == "" {
		return "not set"
	}
	return cfg.DefaultProjectsFolder
}

// managerLabel renders the preferred-manager value the way the UI words it;
// the empty string is "auto".
func managerLabel(m string) string {
	if m == "" {
		return "auto"
	}
	return m
}

// fontLabel is the one-line value shown on the font row.
func fontLabel(cfg config.Config) string {
	if cfg.FontInstalled {
		return "installed"
	}
	return "not installed"
}
