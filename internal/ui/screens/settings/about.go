package settings

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/about"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/logo"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/version"
)

// aboutScreen is the credits page: the wordmark, who made Devpit, where it
// lives, and the build this is. When the background check has found a newer
// release it also says so, with the one command that upgrades this install,
// because a notice that does not say what to do is just a nag.
type aboutScreen struct {
	back key.Binding
}

// newAboutScreen returns the credits sub-screen.
func newAboutScreen() aboutScreen {
	return aboutScreen{
		back: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

// Init implements uictx.Screen.
func (s aboutScreen) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (s aboutScreen) Title() string { return "About" }

// ShortHelp implements uictx.Screen.
func (s aboutScreen) ShortHelp() []key.Binding { return []key.Binding{s.back} }

// FullHelp implements uictx.Screen.
func (s aboutScreen) FullHelp() [][]key.Binding { return [][]key.Binding{{s.back}} }

// Update implements uictx.Screen. Esc is the router's; nothing else to do.
func (s aboutScreen) Update(tea.Msg, uictx.Context) (uictx.Screen, tea.Cmd) { return s, nil }

// View implements uictx.Screen.
func (s aboutScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	ascii := ctx.Icons.Tier == icons.TierASCII

	var b strings.Builder
	b.WriteString(th.Logo.Render(logo.String(ascii)))
	b.WriteString("\n")
	b.WriteString(th.Muted.Render(about.Byline))
	b.WriteString("\n\n")
	b.WriteString(th.Tagline.Render(about.Tagline + "."))
	b.WriteString("\n\n")

	rows := [][2]string{
		{"Version", "v" + version.Short()},
		{"Build", version.Commit + ", " + version.Date},
		{"Author", about.Author + " (" + about.Handle + ")"},
		{"Portfolio", about.Portfolio},
		{"Website", about.Website},
		{"Source", about.Repo},
		{"Issues", about.Issues},
		{"License", about.License + ", free and open source"},
	}
	for _, r := range rows {
		b.WriteString(row(ctx, r[0], r[1]))
		b.WriteByte('\n')
	}

	b.WriteByte('\n')
	if u := ctx.Update; u.Available() {
		b.WriteString(th.Success.Render("Update available: v" + u.Version))
		b.WriteByte('\n')
		b.WriteString(row(ctx, "Upgrade", u.Hint))
		b.WriteByte('\n')
		if u.URL != "" {
			b.WriteString(row(ctx, "Notes", u.URL))
			b.WriteByte('\n')
		}
	} else if ctx.Config.SkipUpdateCheck {
		b.WriteString(th.Muted.Render("Update checks are off. Turn them on in Settings to be told about new releases."))
		b.WriteByte('\n')
	} else {
		b.WriteString(th.Muted.Render("You are on the latest release Devpit knows about. It checks once a day."))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// row draws one labelled line of the credits, the label in the key colour
// and the value in the default text colour, so the values are what the eye
// lands on.
func row(ctx uictx.Context, label, value string) string {
	const labelWidth = 11
	pad := labelWidth - len(label)
	if pad < 1 {
		pad = 1
	}
	return ctx.Theme.Key.Render(label) + strings.Repeat(" ", pad) + ctx.Truncate(value)
}
