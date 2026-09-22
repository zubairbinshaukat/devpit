// Package summary draws the result card that closes every long action:
// what was done, what was skipped and why, and how long it took.
package summary

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Skipped is one item that was not processed, with the reason and the fix.
type Skipped struct {
	// Name is the item, usually a path shortened for display.
	Name string
	// Reason says what happened and what to do next, in plain language.
	Reason string
}

// Result is what an action produced.
type Result struct {
	// Headline is the one-line verdict, e.g. "Freed 8.7 GB".
	Headline string
	// FreedBytes is the space reclaimed, if any.
	FreedBytes uint64
	// Items is how many things were processed.
	Items int
	// Duration is how long the action took.
	Duration time.Duration
	// Skipped lists everything that was left alone.
	Skipped []Skipped
	// Failed marks the whole action as a failure rather than a success.
	Failed bool
}

// DismissedMsg is emitted when the user presses Enter on the card.
type DismissedMsg struct{}

// KeyMap is the card's key bindings.
type KeyMap struct {
	Continue key.Binding
}

// DefaultKeyMap returns the standard binding.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Continue: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "continue")),
	}
}

// ShortHelp returns the bindings for the footer.
func (k KeyMap) ShortHelp() []key.Binding { return []key.Binding{k.Continue} }

// FullHelp returns the bindings grouped for the help overlay.
func (k KeyMap) FullHelp() [][]key.Binding { return [][]key.Binding{{k.Continue}} }

// Model is the summary card.
type Model struct {
	// Result is what to show.
	Result Result
	// Keys are the bindings the card answers to.
	Keys KeyMap
}

// New returns a card for a result.
func New(r Result) Model { return Model{Result: r, Keys: DefaultKeyMap()} }

// Update handles the dismiss key.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if key.Matches(km, m.Keys.Continue) {
		return m, func() tea.Msg { return DismissedMsg{} }
	}
	return m, nil
}

// View renders the card.
func (m Model) View(ctx uictx.Context) string {
	th := ctx.Theme
	r := m.Result
	var b strings.Builder

	mark := th.Success.Render(ctx.Icons.Tick)
	if r.Failed {
		mark = th.Danger.Render(ctx.Icons.Fail)
	}
	b.WriteString(mark)
	b.WriteByte(' ')
	b.WriteString(th.CardTitle.Render(r.Headline))

	stats := make([]string, 0, 3)
	if r.FreedBytes > 0 {
		stats = append(stats, th.Success.Render(header.FormatBytes(r.FreedBytes))+th.Muted.Render(" freed"))
	}
	if r.Items > 0 {
		stats = append(stats, th.Base.Render(fmt.Sprintf("%d", r.Items))+th.Muted.Render(" items"))
	}
	if r.Duration > 0 {
		stats = append(stats, th.Base.Render(formatDuration(r.Duration)))
	}
	if len(stats) > 0 {
		b.WriteString("\n")
		b.WriteString(strings.Join(stats, th.Muted.Render("  ·  ")))
	}

	if len(r.Skipped) > 0 {
		b.WriteString("\n\n")
		b.WriteString(th.Warning.Render(fmt.Sprintf("%s %d skipped", ctx.Icons.Warn, len(r.Skipped))))
		for _, s := range r.Skipped {
			b.WriteString("\n  ")
			b.WriteString(th.Base.Render(s.Name))
			b.WriteString(th.Muted.Render(" — " + s.Reason))
		}
	}

	if e := ctx.Emoji("\U0001F3C1"); e != "" && !r.Failed {
		b.WriteString("\n\n")
		b.WriteString(th.Muted.Render(e + " Back on track."))
	}

	return th.Card.Render(b.String())
}

// formatDuration renders a duration the way the summary shows it: whole
// seconds under a minute, minutes and seconds above.
func formatDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.0f s", d.Seconds())
	default:
		return fmt.Sprintf("%d m %d s", int(d.Minutes()), int(d.Seconds())%60)
	}
}
