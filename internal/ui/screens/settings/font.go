package settings

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/fonts"
	"github.com/zubairbinshaukat/devpit/internal/fonts/setup"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/wt"
)

// wtFallbackFont is the font family appended to Windows Terminal's font
// fallback list. It is setup's constant, not a copy, so this screen and
// `devpit font` can never disagree about what they patch in.
const wtFallbackFont = setup.FallbackFont

// closeTerminalsMessage is shown after a successful install: Windows
// Terminal caches its font list per process (plan.md section 5).
const closeTerminalsMessage = "Close all Windows Terminal windows and reopen to see icons."

// Engine hooks. Production wires these to the real internal/fonts and
// internal/wt packages in New; tests substitute fakes. Each has exactly the
// signature of the matching internal/fonts/setup.Deps field, so the screen
// hands them straight to setup rather than orchestrating them itself.
type (
	fontStatusFunc  func(fonts.Options) (fonts.State, error)
	fontInstallFunc func(context.Context, fonts.Options) (fonts.Result, error)
	fontRemoveFunc  func(fonts.Options) error
	wtFindFunc      func(wt.FindOptions) []wt.Location
	wtPatchFunc     func(path, fallback string) (wt.PatchResult, error)
	wtRestoreFunc   func(path string) error
)

// fontDoneMsg reports the outcome of an install or remove run.
type fontDoneMsg struct {
	action string // "install" or "remove"
	cfg    config.Config

	offline     bool
	manualSteps string
	errText     string

	// patched and manual are set for a successful install: how many
	// Windows Terminal settings files were changed, and which ones could
	// not be patched (usually unparsable) and need the manual snippet
	// instead.
	patched int
	manual  []string
}

// ok reports whether the run succeeded and cfg should be saved.
func (m fontDoneMsg) ok() bool {
	return !m.offline && m.manualSteps == "" && m.errText == ""
}

// fontScreen installs or removes Devpit's icon font and patches every
// Windows Terminal settings.json it can find (plan.md section 5).
type fontScreen struct {
	status  fonts.State
	running bool
	done    bool
	result  fontDoneMsg
	errMsg  string

	// deps is every engine call the screen makes, routed through
	// internal/fonts/setup so install and remove here run exactly the steps
	// `devpit font` (and so web/install.ps1) runs.
	deps setup.Deps

	trigger key.Binding
	back    key.Binding
}

// newFontScreen returns the font sub-screen, reading the current install
// status through statusFn.
func newFontScreen(
	statusFn fontStatusFunc, installFn fontInstallFunc, removeFn fontRemoveFunc,
	findFn wtFindFunc, patchFn wtPatchFunc, restoreFn wtRestoreFunc,
) fontScreen {
	s := fontScreen{
		deps: setup.Deps{
			Status:        statusFn,
			Install:       installFn,
			Remove:        removeFn,
			FindTerminals: findFn,
			Patch:         patchFn,
			Restore:       restoreFn,
		},
		trigger: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "install/remove")),
		back:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
	if st, err := setup.Status(s.deps); err == nil {
		s.status = st
	} else {
		s.errMsg = err.Error()
	}
	return s
}

// Init implements uictx.Screen.
func (s fontScreen) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (s fontScreen) Title() string { return "Icon font" }

// ShortHelp implements uictx.Screen.
func (s fontScreen) ShortHelp() []key.Binding { return []key.Binding{s.trigger, s.back} }

// FullHelp implements uictx.Screen.
func (s fontScreen) FullHelp() [][]key.Binding { return [][]key.Binding{{s.trigger, s.back}} }

// Update implements uictx.Screen.
func (s fontScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if s.running || !key.Matches(msg, s.trigger) {
			return s, nil
		}
		s.running = true
		s.done = false
		s.errMsg = ""
		cfg := ctx.Config
		if s.status.Installed {
			return s, s.startRemove(cfg)
		}
		return s, s.startInstall(cfg)

	case fontDoneMsg:
		s.running = false
		s.done = true
		s.result = msg
		if !msg.ok() {
			s.errMsg = fontFailureText(msg)
			return s, nil
		}
		if st, err := setup.Status(s.deps); err == nil {
			s.status = st
		}
		if msg.action == "remove" {
			return s, tea.Batch(uictx.SaveConfig(msg.cfg), uictx.Status("success", "Font removed"))
		}
		// Save, then ask whether the glyphs actually render before turning
		// the nerd tier on.
		return s, tea.Batch(uictx.SaveConfig(msg.cfg), uictx.Status("success", "Font installed"), uictx.Push(newProbeScreen()))
	}
	return s, nil
}

// fontFailureText turns a failed fontDoneMsg into the line shown to the
// user, per plan.md section 5's three special failure modes.
func fontFailureText(m fontDoneMsg) string {
	switch {
	case m.offline:
		return "No internet connection. Try again later from Settings."
	case m.manualSteps != "":
		return m.manualSteps
	default:
		return m.errText
	}
}

// startInstall runs setup.Install (download and install the font, then
// patch every Windows Terminal settings.json it can find) and folds its
// Outcome into a fontDoneMsg. It runs entirely inside the returned command,
// off the Update/View call stack, so Update never blocks.
func (s fontScreen) startInstall(cfg config.Config) tea.Cmd {
	deps := s.deps
	return func() tea.Msg {
		out, err := setup.Install(context.Background(), deps)
		if err != nil {
			return fontDoneMsg{action: "install", errText: err.Error()}
		}
		return installDoneMsg(out, cfg)
	}
}

// installDoneMsg turns a successful-or-expected setup.Install Outcome into
// the screen's message. Offline and policy-blocked runs carry no cfg, so
// ok() stays false and nothing is saved.
func installDoneMsg(out setup.Outcome, cfg config.Config) fontDoneMsg {
	switch out.Kind {
	case setup.Offline:
		return fontDoneMsg{action: "install", offline: true}
	case setup.PolicyBlocked:
		return fontDoneMsg{action: "install", manualSteps: out.ManualSteps}
	}

	// A file Patch could not handle is left untouched and listed for the
	// manual snippet, per plan.md section 5's edge cases; the font itself
	// is installed either way. setup.Outcome.Apply leaves the icon tier
	// alone: the probe that follows decides.
	msg := fontDoneMsg{action: "install", cfg: out.Apply(cfg)}
	for _, t := range out.Terminals {
		switch {
		case t.Err != nil:
			msg.manual = append(msg.manual, t.Location.Path)
		case t.Changed:
			msg.patched++
		}
	}
	return msg
}

// startRemove runs setup.Remove: restore every Windows Terminal
// settings.json Devpit backed up, then remove the font itself. A restore
// that fails is not shown here: the font removal it precedes still
// succeeded, and a Terminal pointing at a missing fallback font is harmless.
func (s fontScreen) startRemove(cfg config.Config) tea.Cmd {
	deps := s.deps
	return func() tea.Msg {
		out, err := setup.Remove(deps)
		if err != nil {
			return fontDoneMsg{action: "remove", errText: err.Error()}
		}
		return fontDoneMsg{action: "remove", cfg: out.Apply(cfg)}
	}
}

// View implements uictx.Screen.
func (s fontScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	b.WriteString(th.Muted.Render(ctx.Wrap(
		"Symbols Nerd Font Mono is installed for your user only, no admin needed, and added as a fallback to Windows Terminal's font list. Your own font is never replaced.")))
	b.WriteString("\n\n")

	statusLine := "Not installed."
	if s.status.Installed {
		statusLine = "Installed at " + s.status.FontPath
	}
	b.WriteString(th.Base.Render(statusLine))
	b.WriteString("\n\n")

	switch {
	case s.running:
		verb := "Installing"
		if s.status.Installed {
			verb = "Removing"
		}
		b.WriteString(th.Accent.Render(verb + "…"))
	case s.errMsg != "":
		b.WriteString(th.Danger.Render(ctx.Icons.Warn + " " + s.errMsg))
		b.WriteString("\n\n")
		b.WriteString(th.Muted.Render("Enter to try again."))
	case s.done && s.result.ok():
		b.WriteString(s.viewDone(ctx))
	default:
		label := "Enter to install"
		if s.status.Installed {
			label = "Enter to remove"
		}
		b.WriteString(th.Muted.Render(label))
	}

	return b.String()
}

// viewDone renders the confirmation after a successful install or remove.
func (s fontScreen) viewDone(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	if s.result.action == "remove" {
		b.WriteString(th.Success.Render(ctx.Icons.Tick + " Font removed."))
		return b.String()
	}

	b.WriteString(th.Success.Render(ctx.Icons.Tick + " " + closeTerminalsMessage))
	if len(s.result.manual) > 0 {
		b.WriteString("\n\n")
		b.WriteString(th.Warning.Render(fmt.Sprintf(
			"%s %d settings file(s) could not be updated automatically. Paste this into profiles.defaults.font in each:",
			ctx.Icons.Warn, len(s.result.manual))))
		for _, p := range s.result.manual {
			b.WriteString("\n  ")
			b.WriteString(th.Muted.Render(p))
		}
		b.WriteString("\n")
		b.WriteString(th.Base.Render(wt.ManualSnippet(wtFallbackFont)))
	}
	return b.String()
}
