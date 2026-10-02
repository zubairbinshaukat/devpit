package settings

import (
	"errors"
	"io/fs"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/activity"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// skillPhase is where the skill screen is.
type skillPhase int

const (
	skillLooking skillPhase = iota
	skillConfirming
	skillRunning
	skillDone
)

// skillAction is one thing the screen can do.
type skillAction string

const (
	actInstall skillAction = "install"
	actUpdate  skillAction = "update"
	actRemove  skillAction = "remove"
)

// skillStepMsg is one place done, or failed.
type skillStepMsg struct {
	i   int
	err error
}

// skillScreen explains the AI agent skill, shows where it goes and what is
// there now, and installs, updates or removes it behind the usual
// confirmation, which starts on No. Each place is a live row while it runs,
// and a done card says how it went.
type skillScreen struct {
	sess  *skillSession
	phase skillPhase

	actions []skillAction
	pick    int
	action  skillAction
	confirm confirm.Model

	// work is what the run goes through: targets to write, or files to
	// take out. rows are their live rows, errs what went wrong.
	targets []service.AgentTarget
	files   []string
	rows    []activity.Row
	errs    []error

	press, left, right, install, remove key.Binding
}

// newSkillScreen returns the screen over the shared skill session.
func newSkillScreen(sess *skillSession) skillScreen {
	s := skillScreen{
		sess:    sess,
		press:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose")),
		left:    key.NewBinding(key.WithKeys("left", "h", "shift+tab"), key.WithHelp("←→", "move")),
		right:   key.NewBinding(key.WithKeys("right", "l", "tab")),
		install: key.NewBinding(key.WithKeys("i", "u"), key.WithHelp("i", "install/update")),
		remove:  key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "remove")),
	}
	return s.refresh()
}

// refresh works out the actions from what is there now.
func (s skillScreen) refresh() skillScreen {
	s.actions = nil
	st := s.sess.status
	if s.sess.loaded && s.sess.err == nil && st.ClaudeCode {
		if st.CanInstall() {
			if st.Overall() == service.AgentOlder {
				s.actions = append(s.actions, actUpdate)
			} else {
				s.actions = append(s.actions, actInstall)
			}
		}
		if len(st.Removable()) > 0 {
			s.actions = append(s.actions, actRemove)
		}
	}
	s.pick = min(s.pick, max(0, len(s.actions)-1))
	return s
}

// Init implements uictx.Screen. The settings list already looked at the
// skill when it opened; the session it shares holds the answer.
func (s skillScreen) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (s skillScreen) Title() string { return "AI agent skill" }

// Busy implements uictx.BusyReporter: Esc does not leave halfway through.
func (s skillScreen) Busy() bool { return s.phase == skillRunning }

// ShortHelp implements uictx.Screen.
func (s skillScreen) ShortHelp() []key.Binding {
	switch s.phase {
	case skillConfirming:
		return s.confirm.Keys.ShortHelp()
	case skillLooking:
		if len(s.actions) > 1 {
			return []key.Binding{s.press, s.left}
		}
		if len(s.actions) == 1 {
			return []key.Binding{s.press}
		}
	}
	return nil
}

// FullHelp implements uictx.Screen.
func (s skillScreen) FullHelp() [][]key.Binding {
	return [][]key.Binding{{s.press, s.left, s.install, s.remove}}
}

// Update implements uictx.Screen.
func (s skillScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case skillLoadedMsg:
		s.sess.apply(msg)
		if s.phase == skillLooking {
			s = s.refresh()
		}
		return s, nil

	case skillStepMsg:
		return s.step(msg)

	case confirm.AnsweredMsg:
		if s.phase != skillConfirming {
			return s, nil
		}
		if msg.Answer != confirm.AnswerYes {
			s.phase = skillLooking
			return s, nil
		}
		return s.start()

	case tea.KeyPressMsg:
		switch s.phase {
		case skillConfirming:
			next, cmd := s.confirm.Update(msg)
			s.confirm = next
			return s, cmd
		case skillLooking:
			return s.onKey(msg)
		}

	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return s, nil
		}
		lines, at := s.layout(ctx)
		row := ctx.BodyRow(msg.Y)
		switch {
		case s.phase == skillLooking && row == at.buttons && at.buttons >= 0:
			if i, ok := buttonAt(ctx, s.actions, msg.X); ok {
				s.pick = i
				return s.ask(s.actions[i])
			}
		case s.phase == skillConfirming && at.confirm >= 0 && row >= at.confirm && row < len(lines):
			next, cmd := s.confirm.Click(ctx, msg.X-1, row-at.confirm)
			s.confirm = next
			return s, cmd
		}
	}
	return s, nil
}

// onKey handles the keys while the screen shows what is there.
func (s skillScreen) onKey(msg tea.KeyPressMsg) (uictx.Screen, tea.Cmd) {
	if len(s.actions) == 0 {
		return s, nil
	}
	switch {
	case key.Matches(msg, s.left):
		s.pick = max(0, s.pick-1)
	case key.Matches(msg, s.right):
		s.pick = min(len(s.actions)-1, s.pick+1)
	case key.Matches(msg, s.press):
		return s.ask(s.actions[s.pick])
	case key.Matches(msg, s.install):
		for _, a := range s.actions {
			if a == actInstall || a == actUpdate {
				return s.ask(a)
			}
		}
	case key.Matches(msg, s.remove):
		for _, a := range s.actions {
			if a == actRemove {
				return s.ask(a)
			}
		}
	}
	return s, nil
}

// ask shows the confirmation for an action. It starts on No.
func (s skillScreen) ask(a skillAction) (uictx.Screen, tea.Cmd) {
	st := s.sess.status
	s.action = a
	s.targets, s.files = nil, nil
	switch a {
	case actInstall, actUpdate:
		for _, t := range st.Targets {
			if t.State == service.AgentCreate || t.State == service.AgentUpdate {
				s.targets = append(s.targets, t)
			}
		}
		n := len(s.targets)
		if a == actInstall {
			s.confirm = confirm.New("skill", "Install the Devpit skill for Claude Code?",
				"Writes "+places(n)+" listed above. Nothing else changes, and you can remove it here any time.")
		} else {
			s.confirm = confirm.New("skill", "Update the Devpit skill?",
				"Writes the new skill in "+places(n)+". A skill Devpit did not write is never touched.")
		}
	case actRemove:
		s.files = st.Removable()
		s.confirm = confirm.New("skill", "Remove the Devpit skill?",
			"Deletes the SKILL.md Devpit wrote in "+places(len(s.files))+". Skills from anyone else stay.")
	}
	s.phase = skillConfirming
	return s, nil
}

// places is "1 place", "3 places".
func places(n int) string {
	if n == 1 {
		return "SKILL.md in 1 place"
	}
	return "SKILL.md in " + count(n, "place", "places")
}

// start begins the run with the first place.
func (s skillScreen) start() (uictx.Screen, tea.Cmd) {
	s.phase = skillRunning
	s.errs = nil
	s.rows = nil
	for _, t := range s.targets {
		s.rows = append(s.rows, activity.Row{Label: t.Account, Detail: t.File, State: activity.Queued})
	}
	for _, f := range s.files {
		s.rows = append(s.rows, activity.Row{Label: accountOf(s.sess.status, f), Detail: f, State: activity.Queued})
	}
	if len(s.rows) == 0 {
		s.phase = skillDone
		return s, s.sess.load()
	}
	s.rows[0].State = activity.Running
	return s, s.run(0)
}

// run does one place, in a command.
func (s skillScreen) run(i int) tea.Cmd {
	svc := s.sess.svc
	if svc == nil {
		return func() tea.Msg { return skillStepMsg{i: i, err: errors.New("the skill engine is not open")} }
	}
	if i < len(s.targets) {
		t := s.targets[i]
		return func() tea.Msg {
			_, err := svc.AgentInstall([]service.AgentTarget{t})
			return skillStepMsg{i: i, err: err}
		}
	}
	f := s.files[i-len(s.targets)]
	return func() tea.Msg {
		_, err := svc.AgentRemove([]string{f})
		return skillStepMsg{i: i, err: err}
	}
}

// step folds one place's result in and starts the next.
func (s skillScreen) step(msg skillStepMsg) (uictx.Screen, tea.Cmd) {
	if s.phase != skillRunning || msg.i >= len(s.rows) {
		return s, nil
	}
	rows := append([]activity.Row(nil), s.rows...)
	if msg.err != nil {
		rows[msg.i].State = activity.Failed
		rows[msg.i].Detail = plainSkillError(msg.err)
		s.errs = append(s.errs, msg.err)
	} else {
		rows[msg.i].State = activity.Done
	}
	s.rows = rows
	if next := msg.i + 1; next < len(s.rows) {
		s.rows[next].State = activity.Running
		return s, s.run(next)
	}
	s.phase = skillDone
	return s, s.sess.load()
}

// accountOf names the account a removed file belongs to.
func accountOf(st service.AgentStatus, file string) string {
	for _, t := range st.Targets {
		if strings.EqualFold(t.File, file) {
			return t.Account
		}
	}
	return "skill"
}

// plainSkillError says what went wrong in words a person can act on.
func plainSkillError(err error) string {
	msg := err.Error()
	switch {
	case errors.Is(err, service.ErrNoClaudeCode):
		return "Claude Code is no longer on this PC, so there is nowhere to put the skill."
	case errors.Is(err, fs.ErrPermission) || strings.Contains(strings.ToLower(msg), "access is denied"):
		return "Windows would not let Devpit write there (access denied). Close anything using the folder and try again."
	case strings.Contains(msg, "read-only"):
		return "The file is read-only, so Devpit left it as it is. Make it writable and try again."
	case strings.Contains(msg, "changed since the preview"):
		return "That folder changed while this screen was open, so nothing was written there. Press Esc, open the screen again and retry."
	}
	return upperFirst(msg)
}

// upperFirst starts a sentence with a capital letter.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// skillSpots are the body rows a click can land on.
type skillSpots struct {
	buttons, confirm int
}

// View implements uictx.Screen.
func (s skillScreen) View(ctx uictx.Context) string {
	lines, _ := s.layout(ctx)
	return strings.Join(lines, "\n")
}

// layout builds the screen's lines.
func (s skillScreen) layout(ctx uictx.Context) ([]string, skillSpots) {
	th := ctx.Theme
	at := skillSpots{buttons: -1, confirm: -1}
	w := max(40, min(ctx.Width, 100))
	var out []string
	add := func(ls ...string) { out = append(out, ls...) }
	para := func(st lipgloss.Style, indent int, text string) {
		for _, l := range strings.Split(ansi.Wrap(tierText(text, ctx.Icons.Tier == icons.TierASCII), w-indent-1, " "), "\n") {
			add(strings.Repeat(" ", indent) + st.Render(strings.TrimRight(l, " ")))
		}
	}

	add(" " + th.Title.Render("AI agent skill for Claude Code"))
	add(s.stateLines(ctx, w)...)
	add("")

	if s.phase == skillRunning || s.phase == skillDone {
		add(" " + th.Subtitle.Render(runTitle(ctx, s.action, s.phase)))
		add(activity.View(ctx, s.rows, 0, w, max(3, ctx.BodyHeight-len(out)-6)))
		if s.phase == skillDone {
			add("", s.doneLine(ctx))
			add("", " "+th.Muted.Render("Esc goes back to Settings."))
		}
		return out, at
	}

	// The explanation comes first. While the question is up it makes room
	// for the card: the person has read it by then. On a short terminal the
	// two halves lose their headings.
	if s.phase != skillConfirming {
		compact := ctx.BodyHeight > 0 && ctx.BodyHeight < 22
		does := "Check which account each tool uses in a folder and explain why, by running Devpit's " +
			"read-only commands and reading its help pages. It can suggest a change and make it once you agree."
		if compact {
			does = "Lets Claude Code check which account each tool uses in a folder and explain why, " +
				"using Devpit's read-only commands and its help pages."
			para(th.Base, 1, does)
		} else {
			add(" " + th.Subtitle.Render("What it lets Claude Code do"))
			para(th.Base, 3, does)
			add("", " "+th.Subtitle.Render("What it never does"))
		}
		tick := th.Success.Render(ctx.Icons.Tick)
		add("   " + tick + " " + th.Base.Render(fit(ctx, "Never changes anything without your yes: it asks first, every time.", w-6)))
		add("   " + tick + " " + th.Base.Render(fit(ctx, "Never reads account folders, login files or tokens.", w-6)))
		add("")
	}

	if st := s.sess.status; s.sess.loaded && s.sess.err == nil && st.ClaudeCode && len(st.Targets) > 0 {
		add(" " + th.Subtitle.Render("Where it goes"))
		add(s.targetLines(ctx, w)...)
	}

	switch {
	case s.phase == skillConfirming:
		add("")
		at.confirm = len(out)
		cctx := ctx
		cctx.Width = min(w, 90)
		for _, l := range strings.Split(s.confirm.View(cctx), "\n") {
			add(" " + l)
		}
	case len(s.actions) > 0:
		add("")
		at.buttons = len(out)
		add(buttons(ctx, s.actions, s.pick))
	}
	return out, at
}

// stateLines is the one-line summary under the title, with a reason when
// the skill cannot be installed.
func (s skillScreen) stateLines(ctx uictx.Context, w int) []string {
	th, ic := ctx.Theme, ctx.Icons
	ascii := ic.Tier == icons.TierASCII
	dot, ring := "●", "○"
	if ascii {
		dot, ring = "*", "o"
	}
	line := func(st lipgloss.Style, mark, text string) []string {
		ls := strings.Split(ansi.Wrap(tierText(text, ascii), max(16, w-4), " "), "\n")
		out := make([]string, len(ls))
		for i, l := range ls {
			lead := "   "
			if i == 0 {
				lead = " " + st.Render(mark) + " "
			}
			out[i] = lead + th.Base.Render(strings.TrimRight(l, " "))
		}
		return out
	}
	note := func(text string) []string {
		var out []string
		for _, l := range strings.Split(ansi.Wrap(tierText(text, ascii), max(16, w-4), " "), "\n") {
			out = append(out, "   "+th.Muted.Render(strings.TrimRight(l, " ")))
		}
		return out
	}
	switch {
	case !s.sess.loaded:
		return []string{" " + th.Muted.Render("Looking at Claude Code's skills folder"+ellipsis(ctx))}
	case s.sess.err != nil:
		return append(line(th.Danger, ic.Fail, "Devpit could not look at Claude Code's skills folder."),
			note(plainSkillError(s.sess.err))...)
	case !s.sess.status.ClaudeCode:
		return append(line(th.Muted, ring, "Claude Code is not installed on this PC, so there is nowhere to put the skill."),
			note("Install Claude Code, then open this screen again.")...)
	}
	st := s.sess.status
	switch st.Overall() {
	case service.AgentInstalled:
		return line(th.Success, dot, "Installed. Claude Code can use Devpit.")
	case service.AgentViaLink:
		return line(th.Success, dot, "Installed through a shared link: Claude Code reads it from another skills folder.")
	case service.AgentOlder:
		return line(th.Warning, ic.Warn, "Update available: this Devpit has a newer skill than the one Claude Code has.")
	case service.AgentInTheWay:
		why := "Remove or rename that devpit folder yourself if you want Devpit's skill instead."
		return append(line(th.Danger, ic.Fail, "A different skill called devpit is already there, so Devpit will not install its own."),
			note(why)...)
	}
	return line(th.Muted, ring, "Not installed.")
}

// targetLines lists each place: the account, the file, and what is there.
func (s skillScreen) targetLines(ctx uictx.Context, w int) []string {
	th := ctx.Theme
	ts := s.sess.status.Targets
	accW := 0
	stateW := 0
	for _, t := range ts {
		accW = max(accW, ansi.StringWidth(t.Account))
		stateW = max(stateW, ansi.StringWidth(targetState(ctx, t).text))
	}
	accW = min(accW, 16)
	longest := 0
	for _, t := range ts {
		longest = max(longest, ansi.StringWidth(t.File))
	}
	pathW := max(12, min(longest, w-3-accW-2-2-stateW-1))
	var out []string
	for _, t := range ts {
		acc := fit(ctx, t.Account, accW)
		p := shortenPath(t.File, pathW, ellipsis(ctx))
		st := targetState(ctx, t)
		out = append(out, "   "+th.Base.Render(acc+strings.Repeat(" ", accW-ansi.StringWidth(acc)))+"  "+
			th.Info.Render(p+strings.Repeat(" ", pathW-ansi.StringWidth(p)))+"  "+st.style.Render(st.text))
		if t.State == service.AgentForeign && t.Note != "" {
			out = append(out, strings.Repeat(" ", 5+accW)+th.Muted.Render(fit(ctx, upperFirst(t.Note), w-6-accW)))
		}
	}
	return out
}

// targetState is one place's state as a shape and a word.
func targetState(ctx uictx.Context, t service.AgentTarget) piece {
	th, ic := ctx.Theme, ctx.Icons
	dot, ring := "●", "○"
	if ic.Tier == icons.TierASCII {
		dot, ring = "*", "o"
	}
	switch t.State {
	case service.AgentCurrent:
		return piece{dot + " installed", th.Success}
	case service.AgentNewer:
		return piece{dot + " newer, left as is", th.Success}
	case service.AgentUpdate:
		return piece{ic.Warn + " older", th.Warning}
	case service.AgentForeign:
		return piece{ic.Fail + " not Devpit's", th.Danger}
	case service.AgentShared:
		with := "a shared link"
		if t.SharedWith != "" {
			with = t.SharedWith
		}
		return piece{dot + " shared from " + with, th.Info}
	}
	return piece{ring + " not installed", th.Muted}
}

// runTitle heads the live rows.
func runTitle(ctx uictx.Context, a skillAction, p skillPhase) string {
	if p == skillDone {
		return "What happened"
	}
	switch a {
	case actRemove:
		return "Removing" + ellipsis(ctx)
	case actUpdate:
		return "Updating" + ellipsis(ctx)
	}
	return "Installing" + ellipsis(ctx)
}

// doneLine is the card at the end of a run.
func (s skillScreen) doneLine(ctx uictx.Context) string {
	th, ic := ctx.Theme, ctx.Icons
	t := activity.Count(s.rows)
	var text string
	switch {
	case t.Failed > 0 && t.Done == 0:
		return " " + th.Danger.Render(ic.Fail+" Nothing changed. ") + th.Base.Render("The reason is on the row above.")
	case s.action == actRemove:
		text = "Removed from " + count(t.Done, "place", "places") + ". Claude Code no longer has the Devpit skill there."
	default:
		text = "Done in " + count(t.Done, "place", "places") + ". Claude Code picks the skill up the next time it starts."
	}
	out := " " + th.Success.Render(ic.Tick+" ") + th.Base.Render(text)
	if t.Failed > 0 {
		out += "\n " + th.Warning.Render(ic.Warn+" ") + th.Base.Render(count(t.Failed, "place", "places")+" did not work; the reason is on its row.")
	}
	return out
}

// buttons draws the actions as buttons, the chosen one on the band.
func buttons(ctx uictx.Context, acts []skillAction, pick int) string {
	th := ctx.Theme
	parts := make([]string, len(acts))
	for i, a := range acts {
		label := "[ " + buttonLabel(a) + " ]"
		if i == pick {
			parts[i] = th.Selected.Render(label)
		} else {
			parts[i] = th.Muted.Render(label)
		}
	}
	return " " + strings.Join(parts, "   ")
}

// buttonLabel is an action's button text.
func buttonLabel(a skillAction) string {
	switch a {
	case actUpdate:
		return "Update"
	case actRemove:
		return "Remove"
	}
	return "Install"
}

// buttonAt maps a column on the button line to an action.
func buttonAt(_ uictx.Context, acts []skillAction, x int) (int, bool) {
	col := 1
	for i, a := range acts {
		w := ansi.StringWidth("[ " + buttonLabel(a) + " ]")
		if x >= col && x < col+w {
			return i, true
		}
		col += w + 3
	}
	return 0, false
}
