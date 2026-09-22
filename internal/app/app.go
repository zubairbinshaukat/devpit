// Package app wires Devpit together: the root Bubble Tea model, the screen
// stack it routes messages through, and the global key bindings.
//
// The startup path is deliberately empty of work. Building the model reads no
// files beyond the config the caller already loaded, execs nothing and opens
// no sockets. Everything that has to ask the operating system a question,
// starting with free disk space, is a [tea.Cmd] returned from Init and
// therefore runs on its own goroutine after the first frame is on screen.
package app

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	cleanengine "github.com/zubairbinshaukat/devpit/internal/clean"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/footer"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/firstrun"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/home"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// WindowTitle is the terminal window title Devpit sets while it runs.
const WindowTitle = "Devpit"

// StopGrace is how long Ctrl+C waits for a busy screen to finish the item it
// already started before quitting anyway. Nothing is left half-removed inside
// that window: the delete engine renames before it removes, so the worst an
// expired grace period leaves behind is a tombstone the next startup sweep
// finishes (plan.md section 10, "Ctrl+C during delete").
const StopGrace = 10 * time.Second

// stopPoll is how often the quit path re-asks a busy screen whether it is
// done. It is a poll rather than a channel because uictx.Screen deliberately
// has no way for a screen to signal the root model except through messages.
const stopPoll = 100 * time.Millisecond

// SweepFunc finishes the tombstones an interrupted delete left behind. It
// matches [cleanengine.Sweep], which is what production uses.
type SweepFunc func(ctx context.Context, roots []string, opts cleanengine.Options, progress func(cleanengine.Progress)) cleanengine.Report

// Options configure a new app model.
type Options struct {
	// Config is the loaded configuration.
	Config config.Config
	// Warning, when non-empty, is shown once in the footer: it is how a
	// quarantined config file gets reported.
	Warning string
	// ForceASCII reflects the --ascii flag.
	ForceASCII bool
	// Env reads environment variables. Tests pass a fake; production passes
	// nil, which means os.Getenv.
	Env icons.Environ
	// SaveConfig persists the configuration. Production leaves it nil, which
	// means config.Save; tests pass a capture so no test can write to the
	// developer's real settings file.
	SaveConfig func(config.Config) error
	// Sweep finishes interrupted deletes at startup. Production leaves it
	// nil, which means clean.Sweep; tests pass a stub so no test walks the
	// developer's real projects folder.
	Sweep SweepFunc
	// ScreenFactory overrides what a home menu section opens, keyed by the
	// home.Section* identifiers. Production leaves it nil, which means the
	// real screen constructors; the golden tests use it to push screens
	// wired to fakes, so rendering a screen never runs real detection.
	ScreenFactory map[string]func() uictx.Screen
	// ToolVersions reports the node and git versions for the header pills.
	// Production leaves it nil, which means a real lazy detection off the
	// render path; tests pass a stub so no test execs a real binary.
	ToolVersions func(context.Context) header.ToolVersionsMsg
}

// configSaver writes the configuration. It is a field so tests can capture
// saves instead of touching the user's real config file.
type configSaver func(config.Config) error

// Model is Devpit's root Bubble Tea model.
type Model struct {
	opts   Options
	keys   GlobalKeyMap
	router *Router
	header header.Model
	footer footer.Model
	save   configSaver

	cfg      config.Config
	dark     bool
	stopping bool
	stopBy   time.Time
	iconSet  icons.Set
	width    int
	height   int
	sized    bool
	showHelp bool
	quitting bool
}

// New builds the root model. It does no I/O.
func New(opts Options) Model {
	cfg := opts.Config
	cfg.Normalize()

	// Dark is the assumption until the terminal answers
	// tea.RequestBackgroundColor; most developer terminals are dark, so the
	// first frame is usually already correct.
	m := Model{
		opts:    opts,
		keys:    DefaultGlobalKeyMap(),
		header:  header.New(),
		footer:  footer.New(),
		save:    config.Save,
		cfg:     cfg,
		dark:    true,
		iconSet: resolveIcons(cfg, opts),
	}
	if opts.SaveConfig != nil {
		m.save = opts.SaveConfig
	}
	if opts.Warning != "" {
		m.footer = m.footer.SetStatus("warning", "Settings reset — press ? for details")
	}
	m.router = NewRouter(m.rootScreen())
	return m
}

// rootScreen is first run on a fresh install and home after that.
func (m Model) rootScreen() uictx.Screen {
	if !m.cfg.FirstRunDone {
		return firstrun.New(m.cfg)
	}
	return home.New(m.iconSet).WithFactories(m.opts.ScreenFactory)
}

// resolveIcons applies the tier rule to the config and environment.
func resolveIcons(cfg config.Config, opts Options) icons.Set {
	env := opts.Env
	if env == nil {
		env = os.Getenv
	}
	return icons.Resolve(icons.Prefs{
		Tier:            icons.Tier(cfg.Icons),
		FontInstalled:   cfg.FontInstalled,
		GlyphsConfirmed: cfg.GlyphsConfirmed,
		ForceASCII:      opts.ForceASCII,
	}, env)
}

// Init implements tea.Model. The two commands it returns are the only work
// Devpit starts, and both run off the render path.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.RequestBackgroundColor,
		header.DetectDiskCmd(),
		m.toolVersionsCmd(),
	}
	if c := m.sweepCmd(); c != nil {
		cmds = append(cmds, c)
	}
	if s, ok := m.router.Top(); ok {
		if c := s.Init(); c != nil {
			cmds = append(cmds, c)
		}
	}
	return tea.Batch(cmds...)
}

// toolVersionsCmd fills the header's node and git pills. Detection is a
// LookPath plus a --version with a short timeout, on Bubble Tea's own
// goroutine, so the first frame is never held up by it.
func (m Model) toolVersionsCmd() tea.Cmd {
	detect := m.opts.ToolVersions
	if detect == nil {
		detect = detectToolVersions
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return detect(ctx)
	}
}

// detectToolVersions is the production detector behind toolVersionsCmd.
func detectToolVersions(ctx context.Context) header.ToolVersionsMsg {
	d := tools.New()
	node := d.Get(ctx, "node")
	git := d.Get(ctx, "git")
	msg := header.ToolVersionsMsg{Node: "none", Git: "none"}
	if node.Found {
		msg.Node = versionOr(node.Version, "found")
	}
	if git.Found {
		msg.Git = versionOr(git.Version, "found")
	}
	return msg
}

// versionOr returns v, or fallback when the version could not be parsed.
func versionOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// sweepCmd finishes whatever an interrupted delete left behind, on its own
// goroutine, after the first frame. It runs only past first run, reports only
// when it actually finished something, and returns nil when there is nowhere
// to look, so startup is never blocked or noisier than it has to be
// (plan.md section 7 step 5, decision 15.3).
func (m Model) sweepCmd() tea.Cmd {
	if !m.cfg.FirstRunDone {
		return nil
	}
	roots := sweepRoots(m.cfg)
	if len(roots) == 0 {
		return nil
	}
	sweep := m.opts.Sweep
	if sweep == nil {
		sweep = cleanengine.Sweep
	}
	opts := cleanengine.Options{NeverTouch: append([]string(nil), m.cfg.NeverTouch...)}
	return func() tea.Msg {
		report := sweep(context.Background(), roots, opts, nil)
		n := len(report.Deleted)
		if n == 0 {
			return nil
		}
		noun := "deletes"
		if n == 1 {
			noun = "delete"
		}
		return uictx.StatusMsg{
			Level: "success",
			Text:  fmt.Sprintf("Finished %d interrupted %s", n, noun),
		}
	}
}

// sweepRoots is where the startup sweep looks: the default projects folder
// and every folder scanned recently, de-duplicated case-insensitively.
func sweepRoots(cfg config.Config) []string {
	seen := make(map[string]bool, len(cfg.RecentFolders)+1)
	out := make([]string, 0, len(cfg.RecentFolders)+1)
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[strings.ToLower(p)] {
			return
		}
		seen[strings.ToLower(p)] = true
		out = append(out, p)
	}
	add(cfg.DefaultProjectsFolder)
	for _, p := range cfg.RecentFolders {
		add(p)
	}
	return out
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.sized = true
		return m, nil

	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		return m, nil

	case tea.KeyPressMsg:
		if handled, next, cmd := m.globalKey(msg); handled {
			return next, cmd
		}

	case uictx.PushScreenMsg:
		m.router.Push(msg.Screen)
		if msg.Screen != nil {
			return m, msg.Screen.Init()
		}
		return m, nil

	case uictx.PopScreenMsg:
		m.router.Pop()
		return m, nil

	case uictx.ReplaceScreenMsg:
		m.router.Replace(msg.Screen)
		if msg.Screen != nil {
			return m, msg.Screen.Init()
		}
		return m, nil

	case uictx.ConfigChangedMsg:
		return m.applyConfig(msg)

	case uictx.StatusMsg:
		m.footer = m.footer.SetStatus(msg.Level, msg.Text)
		return m, nil

	case firstrun.DoneMsg:
		return m.applyConfig(uictx.ConfigChangedMsg{Config: msg.Config, Persist: true})

	case stopPollMsg:
		return m.pollStop()
	}

	// Components that fold in background results.
	var cmds []tea.Cmd
	nextHeader, cmd := m.header.Update(msg)
	m.header = nextHeader
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	if s, ok := m.router.Top(); ok {
		next, scmd := s.Update(msg, m.context())
		m.router.SetTop(next)
		if scmd != nil {
			cmds = append(cmds, scmd)
		}
	}
	return m, tea.Batch(cmds...)
}

// globalKey handles the bindings that work everywhere. It returns handled=true
// when the key was consumed and must not reach the screen.
func (m Model) globalKey(msg tea.KeyPressMsg) (bool, tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.ForceQuit):
		return m.forceQuit()

	case m.showHelp:
		// While the overlay is up it eats every key, so no screen can act on
		// a keystroke the user meant for the help list.
		if key.Matches(msg, m.keys.Help) || key.Matches(msg, m.keys.Back) {
			m.showHelp = false
		}
		return true, m, nil

	case key.Matches(msg, m.keys.Help):
		m.showHelp = true
		return true, m, nil

	case key.Matches(msg, m.keys.Back):
		// A busy screen owns Esc: on the cleaner it means "stop after the
		// item in flight", which is a different thing from "go back", and
		// popping the screen out from under a running delete would throw
		// away the progress report the user is waiting for. handled=false
		// lets the message fall through to the screen's own Update.
		if m.topBusy() {
			return false, m, nil
		}
		if m.router.Pop() {
			m.footer = m.footer.SetStatus("", "")
			return true, m, nil
		}
		return true, m, nil

	case key.Matches(msg, m.keys.Quit) && m.router.Len() == 1:
		m.quitting = true
		return true, m, tea.Quit
	}
	return false, m, nil
}

// stopPollMsg re-asks a stopping screen whether it has finished.
type stopPollMsg struct{}

// stopPollCmd schedules the next poll.
func stopPollCmd() tea.Cmd {
	return tea.Tick(stopPoll, func(time.Time) tea.Msg { return stopPollMsg{} })
}

// topBusy reports whether the visible screen has work in flight.
func (m Model) topBusy() bool {
	s, ok := m.router.Top()
	return ok && uictx.Busy(s)
}

// forceQuit is Ctrl+C. With nothing running it quits at once, as it always
// has. Over a busy screen it asks the screen to stop, says so in the footer
// and starts polling: Devpit leaves once the screen reports it is done, or
// after [StopGrace], whichever comes first. A second Ctrl+C gives up waiting
// immediately, which is safe because the delete engine renames before it
// removes.
func (m Model) forceQuit() (bool, tea.Model, tea.Cmd) {
	if m.stopping || !m.topBusy() {
		m.quitting = true
		return true, m, tea.Quit
	}
	if s, ok := m.router.Top(); ok {
		uictx.Stop(s)
	}
	m.stopping = true
	m.stopBy = time.Now().Add(StopGrace)
	m.footer = m.footer.SetStatus("warning", "finishing the item in flight…")
	return true, m, stopPollCmd()
}

// pollStop quits as soon as the screen that was asked to stop reports it is
// no longer busy, and gives up waiting when the grace period expires.
func (m Model) pollStop() (tea.Model, tea.Cmd) {
	if !m.stopping {
		return m, nil
	}
	if m.topBusy() && time.Now().Before(m.stopBy) {
		return m, stopPollCmd()
	}
	m.quitting = true
	return m, tea.Quit
}

// terminalProgress is the bar the terminal itself shows in its taskbar while
// a screen is working. Only a busy screen that knows its own progress gets
// one, so an idle menu never leaves a stale bar behind.
func (m Model) terminalProgress() *tea.ProgressBar {
	s, ok := m.router.Top()
	if !ok || !uictx.Busy(s) {
		return nil
	}
	p, ok := s.(uictx.ProgressReporter)
	if !ok {
		return nil
	}
	return p.TerminalProgress()
}

// applyConfig stores a new configuration, rebuilds everything derived from it
// and persists it when asked.
func (m Model) applyConfig(msg uictx.ConfigChangedMsg) (tea.Model, tea.Cmd) {
	cfg := msg.Config
	cfg.Normalize()

	wasFirstRun := !m.cfg.FirstRunDone
	m.cfg = cfg
	m.iconSet = resolveIcons(cfg, m.opts)

	if wasFirstRun && cfg.FirstRunDone {
		m.router.Reset(home.New(m.iconSet))
	}

	if !msg.Persist {
		return m, nil
	}
	save := m.save
	return m, func() tea.Msg {
		if err := save(cfg); err != nil {
			return uictx.StatusMsg{
				Level: "danger",
				Text:  "Could not save settings: " + err.Error(),
			}
		}
		return nil
	}
}

// context builds the render context handed to screens.
func (m Model) context() uictx.Context {
	th := theme.Resolve(m.cfg.Theme, m.dark)
	body := m.height - m.header.Height() - m.footer.Height()
	if body < 0 {
		body = 0
	}
	return uictx.Context{
		Theme:      th,
		Icons:      m.iconSet,
		Config:     m.cfg,
		Width:      m.width,
		Height:     m.height,
		BodyHeight: body,
	}
}

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = WindowTitle
	v.ProgressBar = m.terminalProgress()
	return v
}

// render builds the frame: header, body, footer.
func (m Model) render() string {
	if !m.sized || m.quitting {
		return ""
	}
	ctx := m.context()

	if ctx.Width < uictx.MinWidth || ctx.Height < uictx.MinHeight {
		return m.renderResizeNotice(ctx)
	}

	screen, ok := m.router.Top()
	if !ok {
		return ""
	}

	hdr := m.header
	hdr.Title = screen.Title()

	var body string
	var bindings []key.Binding
	if m.showHelp {
		body = m.renderHelp(ctx, screen)
		bindings = []key.Binding{m.keys.Help, m.keys.ForceQuit}
	} else {
		body = screen.View(ctx)
		bindings = append(screen.ShortHelp(), m.keys.Help)
		// The global Esc hint is only worth showing when the screen has not
		// already claimed that key. A screen that has says something truer
		// about it: on the cleaner mid-scan, Esc is "stop", not "back".
		if m.router.Len() > 1 && !claimsKeys(bindings, m.keys.Back) {
			bindings = append([]key.Binding{m.keys.Back}, bindings...)
		}
	}

	var b strings.Builder
	b.WriteString(hdr.View(ctx))
	b.WriteByte('\n')
	b.WriteString(pad(body, ctx.BodyHeight))
	b.WriteByte('\n')
	b.WriteString(m.footer.View(ctx, bindings))
	return b.String()
}

// claimsKeys reports whether any of bindings is bound to exactly the same
// keys as b, which is how a screen says "this key is mine".
func claimsKeys(bindings []key.Binding, b key.Binding) bool {
	for _, have := range bindings {
		if slices.Equal(have.Keys(), b.Keys()) {
			return true
		}
	}
	return false
}

// renderHelp draws the "?" overlay: the screen's own groups, then the global
// ones.
func (m Model) renderHelp(ctx uictx.Context, screen uictx.Screen) string {
	var b strings.Builder
	b.WriteString(ctx.Theme.Title.Render("Keyboard shortcuts"))
	b.WriteString("\n\n")

	groups := append(screen.FullHelp(), m.keys.FullHelp()...)
	b.WriteString(m.footer.FullHelpView(ctx, groups))

	if m.opts.Warning != "" {
		b.WriteString("\n\n")
		b.WriteString(ctx.Theme.Warning.Render(m.opts.Warning))
	}
	b.WriteString("\n\n")
	b.WriteString(ctx.Theme.Muted.Render("Press ? or Esc to close."))
	return b.String()
}

// renderResizeNotice is what a too-small terminal gets instead of a garbled
// layout.
func (m Model) renderResizeNotice(ctx uictx.Context) string {
	th := ctx.Theme
	msg := th.Notice.Render(
		"Devpit needs a bigger window.\n\n" +
			"Now: " + strconv.Itoa(ctx.Width) + "×" + strconv.Itoa(ctx.Height) + "\n" +
			"Needs: " + strconv.Itoa(uictx.MinWidth) + "×" + strconv.Itoa(uictx.MinHeight) + "\n\n" +
			"Resize the terminal and this will redraw itself.",
	)
	if ctx.Width <= 0 || ctx.Height <= 0 {
		return msg
	}
	return lipgloss.Place(ctx.Width, ctx.Height, lipgloss.Center, lipgloss.Center, msg)
}

// pad grows or trims a body to exactly n rows so the footer never floats.
func pad(body string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(body, "\n")
	if len(lines) > n {
		return strings.Join(lines[:n], "\n")
	}
	return body + strings.Repeat("\n", n-len(lines))
}
