package accounts

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The preview of every change is the engine's own words, laid out the same
// way wherever it is shown: the sentences first (the answer to "what will
// this do?"), then the folders that keep their own rule, the warnings, and
// last the exact files and lines Devpit writes. Nothing here rewords a
// sentence; it only gives each part its place and colour.

// previewDoc is a preview as parts, ready to lay out.
type previewDoc struct {
	sentences []string
	keptIntro string
	kept      [][2]string
	warnings  []string
	notes     []string
	edits     []accounts.FileEdit
	// noChange: the change would change nothing; there is nothing to say
	// yes to.
	noChange bool
}

// docOf is an accounts.Preview as parts.
func docOf(p accounts.Preview) previewDoc {
	d := previewDoc{sentences: p.Sentences, keptIntro: p.KeptIntro, warnings: p.Warnings, noChange: p.NoChange}
	for _, k := range p.Kept {
		d.kept = append(d.kept, [2]string{k.Folder, k.Display})
	}
	if !p.NoChange {
		d.edits = p.Edits
	}
	return d
}

// render lays the parts out in the screen's width.
func (d previewDoc) render(ctx uictx.Context) []string {
	th := ctx.Theme
	var out []string
	for i, s := range d.sentences {
		st := th.Base
		if i == 0 {
			st = th.Base.Bold(true)
		}
		out = append(out, wrap(ctx, st, s, 1, 0)...)
	}
	if len(d.kept) > 0 {
		out = append(out, "")
		out = append(out, wrap(ctx, th.Base, d.keptIntro, 1, 0)...)
		w := 0
		for _, k := range d.kept {
			w = max(w, len([]rune(k[0])))
		}
		w = min(w, max(10, ctx.Width/2))
		for _, k := range d.kept {
			folder := fitPath(ctx, k[0], w)
			out = append(out, "   "+th.Info.Render(folder)+pad(w-len([]rune(folder))+3)+th.Base.Render(fit(ctx, k[1], ctx.Width-w-8)))
		}
	}
	for _, wn := range d.warnings {
		out = append(out, "")
		out = append(out, wrap(ctx, th.Warning, ctx.Icons.Warn+" "+wn, 1, 2)...)
	}
	for _, n := range d.notes {
		out = append(out, wrap(ctx, th.Muted, n, 1, 0)...)
	}
	if len(d.edits) > 0 {
		out = append(out, "", " "+th.Muted.Render("Devpit will write:"))
		for _, e := range d.edits {
			out = append(out, "   "+th.Info.Render(fit(ctx, e.Path, ctx.Width-16))+th.Muted.Render(" ("+e.Action+")"))
			for _, l := range e.Lines {
				out = append(out, "     "+th.Base.Render(fit(ctx, l, ctx.Width-7)))
			}
		}
	}
	return out
}

// confirmPane is a preview with the yes/no card under it: the house shape
// of every change in Accounts. The card's default is No, as every confirm
// in Devpit; the preview scrolls when it is taller than the room above the
// card.
type confirmPane struct {
	doc    previewDoc
	dialog confirm.Model
	scroll int
	// ask is false when there is nothing to say yes to (no change, a
	// read-only window); then the pane shows why instead of the card.
	ask    bool
	reason string
}

// newConfirmPane returns a pane asking question, with detail under it.
func newConfirmPane(id string, doc previewDoc, question, detail string) confirmPane {
	return confirmPane{doc: doc, dialog: confirm.New(id, question, detail), ask: !doc.noChange}
}

// readOnly turns the card into a sentence: this window cannot change
// anything right now.
func (p confirmPane) readOnly(why string) confirmPane {
	if why != "" && p.ask {
		p.ask, p.reason = false, why
	}
	return p
}

// withWord makes the card ask for a typed word before Yes.
func (p confirmPane) withWord(w string) confirmPane {
	if w != "" {
		p.dialog = p.dialog.WithTypedWord(w)
	}
	return p
}

// layout is the pane drawn into height lines, and the row the card starts
// on, for clicks.
func (p confirmPane) layout(ctx uictx.Context, height int) (lines []string, cardTop int) {
	body := p.doc.render(ctx)
	var foot []string
	switch {
	case p.ask:
		foot = strings.Split(p.dialog.View(ctx), "\n")
	case p.reason != "":
		foot = wrap(ctx, ctx.Theme.Warning, ctx.Icons.Warn+" "+p.reason, 1, 2)
	default:
		foot = []string{" " + ctx.Theme.Muted.Render("Nothing to change. Press Enter to go back.")}
	}
	room := height - len(foot) - 1
	if room < 3 {
		room = 3
	}
	if len(body) > room {
		start := min(max(0, p.scroll), len(body)-room+1)
		end := start + room - 1
		more := len(body) - end
		body = append(append([]string(nil), body[start:end]...),
			" "+ctx.Theme.Muted.Render(moreMark(ctx, more)))
	}
	lines = append(append(body, ""), foot...)
	return lines, len(body) + 1
}

// moreMark says how many preview lines are below the fold.
func moreMark(ctx uictx.Context, n int) string {
	if n <= 0 {
		return ""
	}
	arrow := "▼"
	if ctx.Icons.Tier == "ascii" {
		arrow = "v"
	}
	return arrow + " " + plural(n, "1 more line", "%d more lines") + " (↓ to scroll)"
}

// update handles the card's keys and the scroll keys.
func (p confirmPane) update(msg tea.Msg) (confirmPane, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch km.String() {
		case "down", "pgdown":
			p.scroll++
			return p, nil
		case "up", "pgup":
			p.scroll = max(0, p.scroll-1)
			return p, nil
		}
	}
	if wm, ok := msg.(tea.MouseWheelMsg); ok {
		if wm.Button == tea.MouseWheelDown {
			p.scroll++
		} else {
			p.scroll = max(0, p.scroll-1)
		}
		return p, nil
	}
	if !p.ask {
		return p, nil
	}
	next, cmd := p.dialog.Update(msg)
	p.dialog = next
	return p, cmd
}

// click answers the card from a click at body row y (relative to the pane's
// own first line) and column x.
func (p confirmPane) click(ctx uictx.Context, height, x, y int) (confirmPane, tea.Cmd) {
	if !p.ask {
		return p, nil
	}
	_, top := p.layout(ctx, height)
	next, cmd := p.dialog.Click(ctx, x, y-top)
	p.dialog = next
	return p, cmd
}
