// Package settings is the preferences screen.
//
// The settings sit in short groups under headings, most used first: Look,
// Cleaning, Tools, AI agents, Privacy and updates, About. Each row is a
// label and its current value in a column of its own; one place, beside
// the list on a wide terminal and under it on a narrow one, says in plain
// words what the focused setting does and what its values mean.
//
// A value that is one of a few choices cycles in place and is drawn
// ‹ like this ›; on and off are a dot and the word. Enter or Space moves to
// the next choice (←/→ step either way) and saves at once, so there is no
// separate save step to forget, and the row says "✓ saved" for a moment.
//
// Everything else (folders, the never-touch list, dev ports, the day
// counts, the icon font, the icon check, forgetting the last scan, the AI
// agent skill, About and What's new) needs more than one value at a time,
// so Enter on those rows, which end in a chevron, pushes a small private
// sub-screen instead. Esc is handled globally by the router (see
// internal/app), so popping a sub-screen without having pressed its save
// key already discards whatever was half-typed: nothing in this package
// persists a value except through an explicit uictx.SaveConfig call.
package settings

import (
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/fonts"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/choices"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/whatsnew"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/version"
	"github.com/zubairbinshaukat/devpit/internal/wt"
)

// savedFor is how long a row says "✓ saved" after a change.
const savedFor = 1500 * time.Millisecond

// saveNote is the one reassurance the screen keeps on show, quietly.
const saveNote = "Changes save as soon as you make them."

// valueW fixes the value column, so stepping a value never moves it.
const valueW = 20

// Model is the settings screen: the shared choices list over the groups.
type Model struct {
	opts  Options
	list  choices.Model
	skill *skillSession

	// openSkill opens the AI agent skill screen as soon as Settings is up:
	// the What's new card's offer lands there.
	openSkill bool
	// savedSeq tells a stale "✓ saved" fade from the current one.
	savedSeq int

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

// New returns the settings screen over the real engines.
func New() Model { return NewWith(Options{}) }

// NewWith returns the settings screen over the given options.
func NewWith(o Options) Model {
	if o.Skill == nil {
		o.Skill = openSkillService
	}
	if o.Tick == nil {
		o.Tick = tea.Tick
	}
	if o.Version == "" {
		o.Version = version.Short()
	}
	list := choices.New()
	list.Keys.Change.SetHelp("enter", "change/open")
	return Model{
		opts:  o,
		list:  list,
		skill: &skillSession{open: o.Skill},

		fontStatusFn:  fonts.Status,
		fontInstallFn: fonts.Install,
		fontRemoveFn:  fonts.Remove,
		wtFindFn:      wt.FindSettings,
		wtPatchFn:     wt.Patch,
		wtRestoreFn:   wt.Restore,
		clearCacheFn:  clearScanCache,
	}
}

// Init implements uictx.Screen: look at the AI agent skill, off the first
// frame. Each opening of Settings looks again, so Claude Code installed
// since last time shows up.
func (m Model) Init() tea.Cmd {
	if m.openSkill {
		return tea.Batch(m.skill.load(), uictx.Push(newSkillScreen(m.skill)))
	}
	return m.skill.load()
}

// OpeningSkill is Settings that opens the AI agent skill screen on top of
// itself once pushed, sharing what it learns about the skill, so an install
// made there is on the Settings row on the way back.
func (m Model) OpeningSkill() Model {
	m.openSkill = true
	m.list = m.list.SetCursor(rowSkill)
	return m
}

// Title implements uictx.Screen.
func (m Model) Title() string { return "Settings" }

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	return []key.Binding{m.list.Keys.Up, m.list.Keys.Change, m.list.Keys.Left}
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding { return m.list.Keys.FullHelp() }

// savedFadeMsg clears the "✓ saved" mark it was scheduled for.
type savedFadeMsg struct{ seq int }

// spec is what the list shows this frame.
func (m Model) spec(ctx uictx.Context) choices.Spec {
	return choices.Spec{
		Groups:     groups(ctx.Config, m.skill, m.opts.Version),
		Note:       saveNote,
		ValueWidth: valueW,
	}
}

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case skillLoadedMsg:
		m.skill.apply(msg)
		m.list = m.list.Settle(ctx, m.spec(ctx))
		return m, nil
	case savedFadeMsg:
		if msg.seq == m.savedSeq {
			m.list = m.list.MarkSaved("")
		}
		return m, nil
	}
	next, act := m.list.Update(msg, ctx, m.spec(ctx))
	m.list = next
	if act.ID == "" {
		return m, nil
	}
	return m.activate(ctx, act)
}

// activate acts on a row: cycles or flips it and saves, or opens its screen.
func (m Model) activate(ctx uictx.Context, a choices.Act) (uictx.Screen, tea.Cmd) {
	if a.Kind == kindOpen {
		if scr, ok := m.subScreen(a.ID, ctx.Config); ok {
			return m, uictx.Push(scr)
		}
		return m, nil
	}
	cfg, changed := apply(ctx.Config, a.ID, a.Dir)
	if !changed {
		return m, nil
	}
	m.list = m.list.MarkSaved(a.ID)
	m.savedSeq++
	seq := m.savedSeq
	return m, tea.Batch(
		uictx.SaveConfig(cfg),
		m.opts.Tick(savedFor, func(time.Time) tea.Msg { return savedFadeMsg{seq: seq} }),
	)
}

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string { return m.list.View(ctx, m.spec(ctx)) }

// subScreen builds the private sub-screen a row pushes, if it has one.
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
			title: "Recent projects",
			hint: "A project you changed within this many days counts as in use: its junk is still listed, " +
				"but never ticked for you. From 1 to 365 days.",
			value: cfg.ActiveDays,
			apply: func(c config.Config, n int) config.Config { c.ActiveDays = n; return c },
		}), true
	case rowOlderDays:
		return newNumberScreen(numberField{
			title: "Age filter",
			hint: "The age filter on the cleaning results shows only folders nobody touched for more than " +
				"this many days. From 1 to 365 days.",
			value: cfg.OlderDays,
			apply: func(c config.Config, n int) config.Config { c.OlderDays = n; return c },
		}), true
	case rowFont:
		return newFontScreen(m.fontStatusFn, m.fontInstallFn, m.fontRemoveFn, m.wtFindFn, m.wtPatchFn, m.wtRestoreFn), true
	case rowProbe:
		return newProbeScreen(), true
	case rowRescan:
		return newRescanScreen(m.clearCacheFn), true
	case rowSkill:
		return newSkillScreen(m.skill), true
	case rowAbout:
		return newAboutScreen(m.opts.Version), true
	case rowWhatsNew:
		return whatsnew.Reopened(m.opts.Version, whatsNewEntry(m.opts.Version)), true
	default:
		return nil, false
	}
}

// apply cycles one in-place setting by dir (+1 next, -1 previous) and
// reports whether anything changed.
func apply(cfg config.Config, id string, dir int) (config.Config, bool) {
	switch id {
	case rowIcons:
		cfg.Icons = cycle(iconTiers, cfg.Icons, dir)
	case rowTheme:
		cfg.Theme = cycle(themes, cfg.Theme, dir)
	case rowEmoji:
		cfg.Emoji = !cfg.Emoji
	case rowTelemetry:
		cfg.TelemetryOptIn = !cfg.TelemetryOptIn
	case rowManager:
		cfg.PreferredManager = cycle(managers, cfg.PreferredManager, dir)
	case rowUpdates:
		cfg.SkipUpdateCheck = !cfg.SkipUpdateCheck
	default:
		return cfg, false
	}
	return cfg, true
}

// cycle returns the value dir steps away from current in values, wrapping
// around.
func cycle(values []string, current string, dir int) string {
	n := len(values)
	for i, v := range values {
		if v == current {
			return values[((i+dir)%n+n)%n]
		}
	}
	return values[0]
}
