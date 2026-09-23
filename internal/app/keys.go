package app

import "charm.land/bubbles/v2/key"

// GlobalKeyMap holds the bindings that work on every screen. Screens add their
// own on top; these are handled by the root model before a message ever
// reaches a screen, so no screen can accidentally swallow Ctrl+C.
type GlobalKeyMap struct {
	// Back pops one screen off the stack.
	Back key.Binding
	// Help toggles the full shortcut overlay.
	Help key.Binding
	// Quit leaves Devpit from the main menu.
	Quit key.Binding
	// ForceQuit leaves Devpit from anywhere, including mid-action.
	ForceQuit key.Binding
	// NextTab and PrevTab move along the section bar in the header. A
	// screen that binds Tab itself (a form switching fields) keeps it: the
	// root model yields whenever the screen's help lists the same key.
	NextTab key.Binding
	PrevTab key.Binding
	// Jump is 1-7 on the home screen. It is listed here so the help overlay
	// shows it; the home screen handles the keys itself, because a digit
	// typed into a form elsewhere must stay a digit.
	Jump key.Binding
}

// DefaultGlobalKeyMap returns the standard global bindings.
func DefaultGlobalKeyMap() GlobalKeyMap {
	return GlobalKeyMap{
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q"),
			key.WithHelp("q", "quit"),
		),
		ForceQuit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
		NextTab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next section"),
		),
		PrevTab: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "previous section"),
		),
		Jump: key.NewBinding(
			key.WithKeys("1", "2", "3", "4", "5", "6", "7"),
			key.WithHelp("1-7", "open section (home)"),
		),
	}
}

// ShortHelp implements help.KeyMap for the global bindings alone.
func (k GlobalKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Back, k.Help, k.ForceQuit}
}

// FullHelp implements help.KeyMap for the global bindings alone.
func (k GlobalKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.NextTab, k.PrevTab, k.Jump},
		{k.Back, k.Help, k.Quit, k.ForceQuit},
	}
}
