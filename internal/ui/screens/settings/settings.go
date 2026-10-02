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
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/whatsnew"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/version"
	"github.com/zubairbinshaukat/devpit/internal/wt"
)

// savedFor is how long a row says "✓ saved" after a change.
const savedFor = 1500 * time.Millisecond

// keyMap is the list's keys.
type keyMap struct {
	Up, Down, Home, End, PageUp, PageDown key.Binding
	Change, Left, Right                   key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑↓", "move")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓", "down")),
		Home:     key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("home", "first")),
		End:      key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("end", "last")),
		PageUp:   key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
		PageDown: key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdown", "page down")),
		Change:   key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter", "change/open")),
		Left:     key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←→", "choose")),
		Right:    key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→", "next")),
	}
}

// Model is the settings screen.
type Model struct {
	opts  Options
	keys  keyMap
	skill *skillSession

	// cursor is the id of the highlighted row; "" means the first.
	cursor string
	// offset is the first line drawn when the list scrolls.
	offset int
	// openSkill opens the AI agent skill screen as soon as Settings is up:
	// the What's new card's offer lands there.
	openSkill bool
	// savedID is the row showing "✓ saved"; savedSeq tells a stale fade
	// from the current one.
	savedID  string
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
	return Model{
		opts:  o,
		keys:  newKeyMap(),
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
	m.cursor = rowSkill
	return m
}

// Title implements uictx.Screen.
func (m Model) Title() string { return "Settings" }

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	return []key.Binding{m.keys.Up, m.keys.Change, m.keys.Left}
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{m.keys.Up, m.keys.Down, m.keys.Home, m.keys.End, m.keys.PageUp, m.keys.PageDown},
		{m.keys.Change, m.keys.Left, m.keys.Right},
	}
}

// savedFadeMsg clears the "✓ saved" mark it was scheduled for.
type savedFadeMsg struct{ seq int }

// arrange is this frame's layout.
func (m Model) arrange(ctx uictx.Context) layout {
	return arrange(ctx, groups(ctx.Config, m.skill, m.opts.Version), m.cursor, m.offset)
}

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case skillLoadedMsg:
		m.skill.apply(msg)
		return m.settle(ctx), nil

	case savedFadeMsg:
		if msg.seq == m.savedSeq {
			m.savedID = ""
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.onKey(msg, ctx)

	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			return m.move(ctx, -1), nil
		case tea.MouseWheelDown:
			return m.move(ctx, 1), nil
		}
		return m, nil

	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		lo := m.arrange(ctx)
		r, onValue, ok := lo.rowAt(ctx.BodyRow(msg.Y), msg.X)
		if !ok {
			return m, nil
		}
		// A click on a row's value changes it, as does a second click on the
		// row already highlighted; a first click elsewhere only moves there.
		already := r.id == lo.cur
		m.cursor = r.id
		m = m.settle(ctx)
		if onValue || already {
			return m.activate(ctx, r, 1)
		}
		return m, nil

	case tea.MouseMotionMsg:
		// The pointer passing over a row highlights it and shows its
		// description. It never scrolls the list: a row under the pointer is
		// already on screen, so the list stays put under a still pointer.
		lo := m.arrange(ctx)
		if r, _, ok := lo.rowAt(ctx.BodyRow(msg.Y), msg.X); ok && r.id != lo.cur {
			next := m
			next.cursor, next.offset = r.id, lo.start
			if next.arrange(ctx).start == lo.start {
				return next, nil
			}
		}
		return m, nil
	}
	return m, nil
}

// onKey handles the list's keys.
func (m Model) onKey(msg tea.KeyPressMsg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	lo := m.arrange(ctx)
	switch {
	case key.Matches(msg, m.keys.Up):
		return m.move(ctx, -1), nil
	case key.Matches(msg, m.keys.Down):
		return m.move(ctx, 1), nil
	case key.Matches(msg, m.keys.Home):
		return m.move(ctx, -len(lo.sel)), nil
	case key.Matches(msg, m.keys.End):
		return m.move(ctx, len(lo.sel)), nil
	case key.Matches(msg, m.keys.PageUp):
		return m.move(ctx, -max(1, lo.listH/2)), nil
	case key.Matches(msg, m.keys.PageDown):
		return m.move(ctx, max(1, lo.listH/2)), nil
	}
	r, ok := m.focused(lo)
	if !ok {
		return m, nil
	}
	switch {
	case key.Matches(msg, m.keys.Change):
		return m.activate(ctx, r, 1)
	case key.Matches(msg, m.keys.Left) && r.kind != kindOpen:
		return m.activate(ctx, r, -1)
	case key.Matches(msg, m.keys.Right) && r.kind != kindOpen:
		return m.activate(ctx, r, 1)
	}
	return m, nil
}

// move steps the cursor by n rows, stopping at the ends, and scrolls the list
// so it stays on screen. Headings and notes are never stopped on.
func (m Model) move(ctx uictx.Context, n int) Model {
	lo := m.arrange(ctx)
	if len(lo.sel) == 0 {
		return m
	}
	at := 0
	for i, r := range lo.sel {
		if r.id == lo.cur {
			at = i
		}
	}
	at = min(max(0, at+n), len(lo.sel)-1)
	m.cursor = lo.sel[at].id
	return m.settle(ctx)
}

// settle keeps the scroll position that puts the cursor on screen.
func (m Model) settle(ctx uictx.Context) Model {
	lo := m.arrange(ctx)
	m.offset = lo.start
	m.cursor = lo.cur
	return m
}

// activate acts on a row: cycles or flips it and saves, or opens its screen.
// dir is +1 for the next choice and -1 for the one before.
func (m Model) activate(ctx uictx.Context, r row, dir int) (uictx.Screen, tea.Cmd) {
	switch r.kind {
	case kindNote:
		return m, nil
	case kindOpen:
		if scr, ok := m.subScreen(r.id, ctx.Config); ok {
			return m, uictx.Push(scr)
		}
		return m, nil
	}
	cfg, changed := apply(ctx.Config, r.id, dir)
	if !changed {
		return m, nil
	}
	m.savedID = r.id
	m.savedSeq++
	seq := m.savedSeq
	return m, tea.Batch(
		uictx.SaveConfig(cfg),
		m.opts.Tick(savedFor, func(time.Time) tea.Msg { return savedFadeMsg{seq: seq} }),
	)
}

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string {
	return m.view(ctx, m.arrange(ctx))
}

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
