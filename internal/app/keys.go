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
	}
}

// ShortHelp implements help.KeyMap for the global bindings alone.
func (k GlobalKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Back, k.Help, k.ForceQuit}
}

// FullHelp implements help.KeyMap for the global bindings alone.
func (k GlobalKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Back, k.Help, k.Quit, k.ForceQuit}}
}
