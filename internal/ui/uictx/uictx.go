// Package uictx carries what every screen needs to render, and the messages
// screens use to navigate.
//
// It exists so that screens and the router can share types without the screen
// packages importing internal/app, which would be an import cycle.
package uictx

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
)

// MinWidth and MinHeight are the smallest terminal Devpit will draw in.
// Below either, the app shows a resize notice instead of a garbled screen.
const (
	MinWidth  = 80
	MinHeight = 24
)

// Context is the render context handed to every screen's View. It is passed by
// value and is read-only from a screen's point of view: to change the config,
// emit a [ConfigChangedMsg].
type Context struct {
	// Theme holds the palette and styles for the detected background.
	Theme *theme.Theme
	// Icons is the resolved glyph tier.
	Icons icons.Set
	// Config is the current user configuration.
	Config config.Config
	// Width and Height are the terminal size in cells.
	Width, Height int
	// BodyHeight is Height minus the header and footer, i.e. the rows a
	// screen may draw into.
	BodyHeight int
}

// Emoji returns e when the user has emoji enabled, otherwise "".
func (c Context) Emoji(e string) string { return icons.Emoji(c.Config.Emoji, e) }

// Wrap folds a paragraph to the terminal width. Screens run every run of
// prose through it so nothing ever spills past the right edge.
func (c Context) Wrap(s string) string {
	if c.Width <= 1 {
		return s
	}
	return lipgloss.Wrap(s, c.Width, "")
}

// Truncate cuts a single line to the terminal width, marking the cut with an
// ellipsis. It works on plain text, before any style is applied.
func (c Context) Truncate(s string) string {
	if c.Width <= 1 {
		return s
	}
	return ansi.Truncate(s, c.Width, "…")
}

// Screen is one page of the UI. Screens are values: Update returns the next
// Screen rather than mutating the receiver, which keeps the router's stack
// honest.
type Screen interface {
	// Init returns an optional command to run when the screen is pushed.
	Init() tea.Cmd
	// Update handles a message and returns the next state of the screen.
	Update(msg tea.Msg, ctx Context) (Screen, tea.Cmd)
	// View renders the screen body, without header or footer.
	View(ctx Context) string
	// Title is shown in the header breadcrumb.
	Title() string
	// ShortHelp is the key-hint bar for this screen, appended to the global
	// bindings by the footer.
	ShortHelp() []key.Binding
	// FullHelp is the grouped list shown by the "?" overlay.
	FullHelp() [][]key.Binding
}

// BusyReporter is the optional interface a screen implements when it can be
// in the middle of work that must not be interrupted mid-item, such as a
// delete. While a screen reports Busy, the root model stops treating Esc as
// "pop" and forwards it to the screen instead, and Ctrl+C asks the screen to
// wind down before quitting.
//
// A screen that does not implement it is never busy.
type BusyReporter interface {
	// Busy reports that the screen owns work in flight.
	Busy() bool
}

// Stopper is the optional interface a screen implements when it owns work
// that has to be wound down. The router calls Stop on every screen it
// discards, and the root model calls it on a busy screen when the user
// presses Ctrl+C.
//
// Stop must not block: it cancels, it does not wait. Whether the work has
// actually finished is what [BusyReporter.Busy] answers.
type Stopper interface {
	// Stop asks the screen to wind down whatever it started.
	Stop()
}

// ProgressReporter is the optional interface a screen implements when it can
// put its progress on the terminal's own taskbar. The root model reads it
// while the screen is busy and copies the result onto [tea.View.ProgressBar].
type ProgressReporter interface {
	// TerminalProgress is the bar to show in the terminal's taskbar, or nil
	// for none.
	TerminalProgress() *tea.ProgressBar
}

// Busy reports whether s is a [BusyReporter] with work in flight. A nil or
// plain screen is never busy.
func Busy(s Screen) bool {
	b, ok := s.(BusyReporter)
	return ok && b.Busy()
}

// Stop asks s to wind down if it is a [Stopper], and does nothing otherwise.
func Stop(s Screen) {
	if st, ok := s.(Stopper); ok {
		st.Stop()
	}
}

// PushScreenMsg asks the router to push a screen onto the stack.
type PushScreenMsg struct {
	// Screen is the screen to show.
	Screen Screen
}

// PopScreenMsg asks the router to pop the current screen.
type PopScreenMsg struct{}

// ReplaceScreenMsg swaps the top of the stack without growing it. It is how
// the first-run screen hands over to home.
type ReplaceScreenMsg struct {
	// Screen is the screen to show instead of the current one.
	Screen Screen
}

// ConfigChangedMsg carries a new configuration. The router stores it, rebuilds
// the theme and icon set, and saves it to disk when Persist is true.
type ConfigChangedMsg struct {
	// Config is the new configuration.
	Config config.Config
	// Persist asks the router to write the file.
	Persist bool
}

// StatusMsg sets the right-hand status text in the footer. An empty Text
// clears it.
type StatusMsg struct {
	// Text is the message to show.
	Text string
	// Level tints the text: "", "success", "warning" or "danger".
	Level string
}

// Push is a command that pushes s.
func Push(s Screen) tea.Cmd { return func() tea.Msg { return PushScreenMsg{Screen: s} } }

// Pop is a command that pops the current screen.
func Pop() tea.Cmd { return func() tea.Msg { return PopScreenMsg{} } }

// Replace is a command that replaces the current screen with s.
func Replace(s Screen) tea.Cmd { return func() tea.Msg { return ReplaceScreenMsg{Screen: s} } }

// SaveConfig is a command that stores and persists cfg.
func SaveConfig(cfg config.Config) tea.Cmd {
	return func() tea.Msg { return ConfigChangedMsg{Config: cfg, Persist: true} }
}

// Status is a command that sets the footer status text.
func Status(level, text string) tea.Cmd {
	return func() tea.Msg { return StatusMsg{Text: text, Level: level} }
}
