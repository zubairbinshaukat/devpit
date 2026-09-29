// Package confirm is Devpit's yes/no dialog.
//
// Safety rule: the default answer is always No. There is deliberately no way
// to construct a Yes-default dialog, and a unit test asserts it: every
// constructor in this package leaves the cursor on No, and Reset puts it back.
//
// The Careful risk tier uses [Model.WithTypedWord], which additionally makes
// the user type a word (DELETE) before Yes does anything.
package confirm

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Answer is the outcome of a dialog.
type Answer int

// The possible answers.
const (
	// AnswerNo is the default and what Esc produces.
	AnswerNo Answer = iota
	// AnswerYes is only reachable by an explicit confirmation.
	AnswerYes
)

// String renders the answer for logs and tests.
func (a Answer) String() string {
	if a == AnswerYes {
		return "yes"
	}
	return "no"
}

// AnsweredMsg is emitted when the user settles the dialog.
type AnsweredMsg struct {
	// ID identifies which dialog answered, when a screen has more than one.
	ID string
	// Answer is the outcome. It is AnswerNo unless the user explicitly
	// confirmed.
	Answer Answer
}

// KeyMap is the dialog's key bindings.
type KeyMap struct {
	Left    key.Binding
	Right   key.Binding
	Confirm key.Binding
	Yes     key.Binding
	No      key.Binding
}

// DefaultKeyMap returns the standard bindings. Note that "y" alone does not
// confirm a typed-word dialog; the word still has to match.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Left:    key.NewBinding(key.WithKeys("left", "h")),
		Right:   key.NewBinding(key.WithKeys("right", "l")),
		Confirm: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm")),
		Yes:     key.NewBinding(key.WithKeys("y"), key.WithHelp("y/n", "yes/no")),
		No:      key.NewBinding(key.WithKeys("n", "esc")),
	}
}

// ShortHelp returns the bindings for the footer.
func (k KeyMap) ShortHelp() []key.Binding { return []key.Binding{k.Yes, k.Confirm} }

// FullHelp returns the bindings grouped for the help overlay.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Yes, k.Confirm}}
}

// Model is a confirmation dialog.
type Model struct {
	// ID is echoed back in AnsweredMsg.
	ID string
	// Question is the single-sentence prompt.
	Question string
	// Detail is an optional second line, typically the restore hint. Every
	// destructive confirmation in Devpit must set it.
	Detail string
	// Keys are the bindings this dialog answers to.
	Keys KeyMap

	// typedWord, when non-empty, is the word the user must type before Yes
	// becomes available.
	typedWord string
	typed     string

	// yes is the cursor position. It starts false and Reset returns it to
	// false; nothing in this package ever initialises it to true.
	yes bool
}

// New returns a dialog whose default answer is No.
func New(id, question, detail string) Model {
	return Model{
		ID:       id,
		Question: question,
		Detail:   detail,
		Keys:     DefaultKeyMap(),
	}
}

// WithTypedWord requires the user to type word before Yes can be chosen. This
// is the Careful tier's second confirmation.
func (m Model) WithTypedWord(word string) Model {
	m.typedWord = word
	m.typed = ""
	m.yes = false
	return m
}

// Reset returns the dialog to its initial state, with the answer back on No
// and any typed word cleared.
func (m Model) Reset() Model {
	m.yes = false
	m.typed = ""
	return m
}

// Answer is the answer the dialog would produce right now.
func (m Model) Answer() Answer {
	if m.yes && m.wordSatisfied() {
		return AnswerYes
	}
	return AnswerNo
}

// TypedWord returns the word required before Yes, or "" when none is.
func (m Model) TypedWord() string { return m.typedWord }

// Typed returns what the user has typed so far.
func (m Model) Typed() string { return m.typed }

// Update handles the dialog's keys.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}

	// In typed-word mode, printable keys build the word.
	if m.typedWord != "" {
		switch km.String() {
		case "backspace":
			if m.typed != "" {
				r := []rune(m.typed)
				m.typed = string(r[:len(r)-1])
			}
			return m, nil
		default:
			if t := km.Key().Text; t != "" {
				m.typed += t
				m.yes = m.wordSatisfied()
				return m, nil
			}
		}
	}

	switch {
	case key.Matches(km, m.Keys.No):
		return m.Reset(), m.answer(AnswerNo)
	case key.Matches(km, m.Keys.Yes) && m.typedWord == "":
		m.yes = true
		return m, m.answer(AnswerYes)
	case key.Matches(km, m.Keys.Left):
		m.yes = false
	case key.Matches(km, m.Keys.Right):
		m.yes = m.wordSatisfied()
	case key.Matches(km, m.Keys.Confirm):
		return m, m.answer(m.Answer())
	}
	return m, nil
}

// View renders the dialog as a card.
func (m Model) View(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	b.WriteString(th.CardTitle.Render(m.Question))
	if m.Detail != "" {
		b.WriteString("\n")
		b.WriteString(th.Muted.Render(m.Detail))
	}
	b.WriteString("\n\n")

	if m.typedWord != "" {
		b.WriteString(th.Warning.Render("Type " + m.typedWord + " to continue: "))
		b.WriteString(th.Base.Render(m.typed))
		if m.wordSatisfied() {
			b.WriteString(" ")
			b.WriteString(th.Success.Render(ctx.Icons.Tick))
		}
		b.WriteString("\n\n")
	}

	// Two buttons with air between them. The chosen one sits on the
	// selection band; the other is a quiet outline of brackets, so both read
	// as things to press, with a mouse as much as with the keyboard.
	yesStyle := th.Base
	if m.typedWord != "" && !m.wordSatisfied() {
		yesStyle = th.Muted
	}
	if m.yes && m.wordSatisfied() {
		b.WriteString(th.Muted.Render(btnNo))
		b.WriteString(btnGap)
		b.WriteString(th.Selected.Render(btnYes))
	} else {
		b.WriteString(th.Selected.Render(btnNo))
		b.WriteString(btnGap)
		b.WriteString(yesStyle.Render(btnYes))
	}

	card := th.Card
	// A long command in the detail would otherwise stretch the card past
	// the terminal's edge; a card never grows wider than the screen, and
	// what does not fit wraps inside it.
	if ctx.Width > 0 && lipgloss.Width(b.String())+4 > ctx.Width {
		card = card.Width(ctx.Width)
	}
	return card.Render(b.String())
}

// The two buttons and the gap between them. Click hit-tests the same
// strings View draws.
const (
	btnNo  = "[ No ]"
	btnYes = "[ Yes ]"
	btnGap = "   "
)

// Click answers the dialog from a mouse click at column x, row y, both
// relative to the dialog's top-left corner (its card border). A click on No
// answers No; a click on Yes answers Yes, or does nothing while a typed word
// is still owed. Anywhere else does nothing: a stray click never answers.
func (m Model) Click(ctx uictx.Context, x, y int) (Model, tea.Cmd) {
	lines := strings.Split(m.View(ctx), "\n")
	// The card's last line is its bottom border; the buttons are just above.
	if len(lines) < 3 || y != len(lines)-2 {
		return m, nil
	}
	// Border and one column of padding sit before the buttons.
	start := 2
	switch {
	case x >= start && x < start+len(btnNo):
		return m.Reset(), m.answer(AnswerNo)
	case x >= start+len(btnNo)+len(btnGap) && x < start+len(btnNo)+len(btnGap)+len(btnYes):
		if !m.wordSatisfied() {
			return m, nil
		}
		m.yes = true
		return m, m.answer(AnswerYes)
	}
	return m, nil
}

// wordSatisfied reports whether the typed-word requirement is met. It is
// always true when no word was required.
func (m Model) wordSatisfied() bool {
	return m.typedWord == "" || m.typed == m.typedWord
}

// answer builds the command that reports an outcome.
func (m Model) answer(a Answer) tea.Cmd {
	id := m.ID
	return func() tea.Msg { return AnsweredMsg{ID: id, Answer: a} }
}
