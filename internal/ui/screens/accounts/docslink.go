package accounts

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// docsLine is a problem's docs link as one line under its fix, indented like
// the fix, or nil when no page explains the problem. It never wraps: it is
// the full address when that fits, then the address without https:// and
// the label, then the page's name and the site.
func docsLine(ctx uictx.Context, p service.Problem, indent int) []string {
	link := p.Link()
	if link == "" {
		return nil
	}
	room := max(10, ctx.Width-indent-1)
	bare := strings.TrimPrefix(link, "https://")
	slug := link[strings.LastIndex(link, "/")+1:]
	text := "Docs: " + slug
	for _, c := range []string{"Docs: " + link, "Docs: " + bare, bare, "Docs: " + slug + " on devpit.zubyr.dev"} {
		if ansi.StringWidth(c) <= room {
			text = c
			break
		}
	}
	if ansi.StringWidth(text) > room {
		text = ansi.Truncate(text, room, "")
	}
	return []string{pad(indent) + ctx.Theme.Muted.Render(text)}
}
