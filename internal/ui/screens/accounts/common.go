package accounts

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/claudeshare"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/activity"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// bind builds a key binding whose first key is also its hint.
func bind(help string, keys ...string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(keys[0], help))
}

// statusOK is a green footer status.
func statusOK(text string) uictx.StatusMsg { return uictx.StatusMsg{Text: text, Level: "success"} }

// spinMsg advances a screen's spinner.
type spinMsg struct{}

// spinEvery is how often a busy screen advances its spinner.
const spinEvery = 120 * time.Millisecond

// spin schedules the next spinner frame.
func (s *session) spin() tea.Cmd {
	if s == nil || s.opts.Tick == nil {
		return nil
	}
	return s.opts.Tick(spinEvery, func(time.Time) tea.Msg { return spinMsg{} })
}

// runHandle lets a screen's Stop, which must not block, cancel the work its
// commands started. It is a pointer inside a value screen so every copy of
// the screen shares it.
type runHandle struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	// stopped records that Stop ran, for a screen whose Busy depends on it.
	stopped bool
}

// newRunHandle returns a handle and the context it cancels.
func newRunHandle() (*runHandle, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	return &runHandle{cancel: cancel}, ctx
}

// stop cancels the work. It is safe to call twice and on a nil handle.
func (h *runHandle) stop() {
	if h == nil {
		return
	}
	h.mu.Lock()
	c := h.cancel
	h.stopped = true
	h.mu.Unlock()
	if c != nil {
		c()
	}
}

// wasStopped reports whether stop ran.
func (h *runHandle) wasStopped() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stopped
}

// popN pops n screens, one after another.
func popN(n int) tea.Cmd {
	if n <= 1 {
		return uictx.Pop()
	}
	cmds := make([]tea.Cmd, n)
	for i := range cmds {
		cmds[i] = uictx.Pop()
	}
	return tea.Sequence(cmds...)
}

// then pops n screens and hands msg to the screen that is on top after.
func then(n int, msg tea.Msg) tea.Cmd {
	return tea.Sequence(popN(n), func() tea.Msg { return msg })
}

// --- live rows ---

// runEventMsg is one event of a long engine operation, tagged with the run
// it belongs to so a stale one is ignored.
type runEventMsg struct {
	id int
	ev accounts.Event
	ok bool
}

// liveRun turns an engine's event stream into activity rows: one row per
// step, its state following the step's events, and every event also kept
// as a raw log line behind "l".
type liveRun struct {
	id    int
	ch    <-chan accounts.Event
	rows  []activity.Row
	log   []string
	final *accounts.Event
	// warnings are the steps that finished with a warning, for the done
	// card: "Open a new terminal", "gh switched its active account".
	warnings []accounts.Event
}

// runIDs hands out run ids. It is a counter in the session, not a global,
// so tests are deterministic.
func (s *session) nextRun() int {
	s.runs++
	return s.runs
}

// startRun begins reading ch.
func startRun(id int, ch <-chan accounts.Event) liveRun {
	return liveRun{id: id, ch: ch}
}

// wait reads the next event.
func (r liveRun) wait() tea.Cmd {
	ch, id := r.ch, r.id
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		return runEventMsg{id: id, ev: ev, ok: ok}
	}
}

// done reports whether the run has ended.
func (r liveRun) done() bool { return r.final != nil }

// ok reports whether the run ended well.
func (r liveRun) ok() bool { return r.final != nil && r.final.State == accounts.StepDone }

// add folds one event into the rows.
func (r liveRun) add(ev accounts.Event, ok bool) liveRun {
	if !ok {
		if r.final == nil {
			f := accounts.Failed("Saving the change", errors.New("the change ended without saying how it went"))
			r.final = &f
		}
		return r
	}
	line := ev.Step
	switch {
	case ev.Err != nil:
		line += ": " + ev.Err.Error()
	case ev.Detail != "":
		line += ": " + ev.Detail
	}
	r.log = append(append([]string(nil), r.log...), line)
	rows := append([]activity.Row(nil), r.rows...)
	at := -1
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Label == ev.Step {
			at = i
			break
		}
	}
	upsert := func(row activity.Row) {
		if at >= 0 {
			rows[at] = row
			return
		}
		rows = append(rows, row)
	}
	switch ev.State {
	case accounts.StepRunning:
		upsert(activity.Row{Label: ev.Step, State: activity.Running, Percent: -1, Detail: ev.Detail})
	case accounts.StepWaiting:
		d := ev.Detail
		if d == "" {
			d = "waiting for you"
		}
		upsert(activity.Row{Label: ev.Step, State: activity.Running, Percent: -1, Detail: d})
	case accounts.StepDone:
		if !ev.Final || at >= 0 || len(rows) == 0 {
			upsert(activity.Row{Label: ev.Step, State: activity.Done, Detail: ev.Detail})
		}
	case accounts.StepWarning:
		upsert(activity.Row{Label: ev.Step, State: activity.Warn, Detail: ev.Detail})
		r.warnings = append(append([]accounts.Event(nil), r.warnings...), ev)
	case accounts.StepFailed:
		d := ""
		if ev.Err != nil {
			d = ev.Err.Error()
		}
		upsert(activity.Row{Label: ev.Step, State: activity.Failed, Detail: d})
	case accounts.StepInfo:
		// Tool output and hints go to the log; a row that is running shows
		// the latest line.
		for i := len(rows) - 1; i >= 0; i-- {
			if rows[i].State == activity.Running {
				rows[i].Detail = ev.Detail
				break
			}
		}
		if ev.Step != "" && ev.Detail != "" && strings.HasPrefix(ev.Step, "Open a new terminal") {
			r.warnings = append(append([]accounts.Event(nil), r.warnings...), ev)
		}
	}
	// A step still marked running when a later one starts has finished.
	if ev.State == accounts.StepRunning || ev.State == accounts.StepDone {
		for i := range rows {
			if rows[i].State == activity.Running && rows[i].Label != ev.Step {
				rows[i].State = activity.Done
			}
		}
	}
	r.rows = rows
	if ev.Final {
		e := ev
		r.final = &e
		// The run is over: a step still marked running finished (a tool's
		// sign-in that returned, a wait that ended) whichever way it went.
		for i := range rows {
			if rows[i].State == activity.Running {
				rows[i].State = activity.Done
				if rows[i].Detail == "waiting for you" {
					rows[i].Detail = ""
				}
			}
		}
	}
	return r
}

// err is the run's failure, or nil.
func (r liveRun) err() error {
	if r.final == nil || r.final.State == accounts.StepDone {
		return nil
	}
	if r.final.Err != nil {
		return r.final.Err
	}
	return errors.New("the change did not finish")
}

// progress is how far the run has got, for the terminal's taskbar.
func (r liveRun) progress() *tea.ProgressBar {
	if r.done() {
		return nil
	}
	if len(r.rows) == 0 {
		return tea.NewProgressBar(tea.ProgressBarIndeterminate, 0)
	}
	n := 0
	for _, row := range r.rows {
		if row.State.Final() {
			n++
		}
	}
	return tea.NewProgressBar(tea.ProgressBarDefault, min(95, n*100/(len(r.rows)+1)))
}

// view draws the rows, or the raw log.
func (r liveRun) view(ctx uictx.Context, frame, height int, showLog bool) string {
	if showLog {
		if len(r.log) == 0 {
			return ctx.Theme.Muted.Render("  Nothing logged yet.")
		}
		return activity.LogView(ctx, r.log, ctx.Width, height)
	}
	if len(r.rows) == 0 {
		return ctx.Theme.Muted.Render(" " + ctx.SpinnerFrame(frame) + " Starting" + ellipsis(ctx))
	}
	// The engine names its steps in whole sentences ("Recording the change
	// so it can be undone"), so the name column is as wide as they need, up
	// to most of the line; the shared 30-cell cap is for package names.
	labelW := 0
	for _, row := range r.rows {
		labelW = max(labelW, ansi.StringWidth(row.Label))
	}
	labelW = min(labelW, max(20, ctx.Width*3/5))
	rows := r.rows
	if height > 0 && len(rows) > height {
		rows = rows[len(rows)-height:]
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = activity.RowView(ctx, row, frame, ctx.Width, labelW)
	}
	return strings.Join(out, "\n")
}

// --- text ---

// ellipsis is the tier's "…".
func ellipsis(ctx uictx.Context) string {
	if ctx.Icons.Tier == icons.TierASCII {
		return "..."
	}
	return "…"
}

// sep is the tier's " · ".
func sep(ctx uictx.Context) string {
	if ctx.Icons.Tier == icons.TierASCII {
		return " - "
	}
	return " · "
}

// fit cuts plain text to w cells with the tier's ellipsis.
func fit(ctx uictx.Context, s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, ellipsis(ctx))
}

// wrap folds plain text to width, every line after the first indented by
// hang spaces, and styles each line.
func wrap(ctx uictx.Context, st lipgloss.Style, text string, indent, hang int) []string {
	first := max(10, ctx.Width-indent-1)
	rest := max(10, first-hang)
	var out []string
	for _, para := range strings.Split(text, "\n") {
		// ansi.Wrap breaks at spaces and, where a path or a command has none,
		// cuts it hard, so nothing ever runs past the edge.
		lines := strings.Split(ansi.Wrap(para, first, " "), "\n")
		if len(lines) > 1 {
			tail := strings.Join(lines[1:], " ")
			lines = append(lines[:1], strings.Split(ansi.Wrap(tail, rest, " "), "\n")...)
		}
		for i, l := range lines {
			in := indent
			if i > 0 {
				in += hang
			}
			out = append(out, pad(in)+st.Render(strings.TrimRight(l, " ")))
		}
	}
	return out
}

// pad is n spaces.
func pad(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// heading is a screen's first line: the title on the left and, when there
// is room, a quiet note on the right ("in C:\Projects\quiz-slayer").
func heading(ctx uictx.Context, title, right string) string {
	th := ctx.Theme
	left := " " + th.Title.Render(title)
	if right == "" {
		return left
	}
	lw := ansi.StringWidth(" " + title)
	room := ctx.Width - lw - 4
	if room < 12 {
		return left
	}
	right = fitPath(ctx, right, room)
	return left + pad(ctx.Width-lw-ansi.StringWidth(right)-1) + th.Muted.Render(right)
}

// fitPath shortens a path in the middle so its drive and last folder stay.
func fitPath(ctx uictx.Context, p string, w int) string {
	if ansi.StringWidth(p) <= w {
		return p
	}
	// "in C:\Proj…\quiz-slayer": keep a leading word if there is one.
	prefix := ""
	if i := strings.Index(p, " "); i >= 0 && i < 4 {
		prefix, p = p[:i+1], p[i+1:]
	}
	return prefix + shortenPath(p, w-ansi.StringWidth(prefix), ellipsis(ctx))
}

// shortenPath is acctable's middle cut, repeated here so a heading and the
// table shorten a path the same way.
func shortenPath(p string, w int, ell string) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(p) <= w {
		return p
	}
	i := strings.LastIndexAny(p, `\/`)
	j := strings.IndexAny(p, `\/`)
	if i <= j || j < 0 {
		return ansi.Truncate(p, w, ell)
	}
	head, last := p[:j+1], p[i:]
	room := w - ansi.StringWidth(head) - ansi.StringWidth(ell) - ansi.StringWidth(last)
	if room < 0 {
		return ansi.TruncateLeft(p, ansi.StringWidth(p)-w+ansi.StringWidth(ell), ell)
	}
	return head + ansi.Truncate(p[j+1:i], room, "") + ell + last
}

// noticeLine draws one notice: a shape, then the words, cut to one line.
func noticeLine(ctx uictx.Context, n notice) string {
	mark, st := noticeMark(ctx, n.level)
	return " " + st.Render(mark) + " " + ctx.Theme.Base.Render(fit(ctx, n.text, ctx.Width-4))
}

// noticeLines draws a notice wrapped to at most n lines, the last one cut.
func noticeLines(ctx uictx.Context, n notice, most int) []string {
	mark, st := noticeMark(ctx, n.level)
	first := " " + st.Render(mark)
	ls := wrap(ctx, ctx.Theme.Base, n.text, 3, 0)
	if len(ls) > most {
		ls = ls[:most]
		last := ansi.Strip(ls[most-1])
		ls[most-1] = ctx.Theme.Base.Render(fit(ctx, strings.TrimSpace(last)+" "+ellipsis(ctx), ctx.Width-4))
		ls[most-1] = "   " + ls[most-1]
	}
	if len(ls) > 0 {
		ls[0] = first + " " + strings.TrimLeft(ls[0], " ")
	}
	return ls
}

// noticeMark is a notice's shape and colour.
func noticeMark(ctx uictx.Context, level string) (string, lipgloss.Style) {
	th, ic := ctx.Theme, ctx.Icons
	switch level {
	case "success":
		return ic.Tick, th.Success
	case "warning":
		return ic.Warn, th.Warning
	case "danger":
		return ic.Fail, th.Danger
	}
	return ic.Queued, th.Muted
}

// scrolled is lines from offset, cut to room with a "more" mark at the end
// when anything is below.
func scrolled(ctx uictx.Context, ls []string, offset, room int) []string {
	if room <= 0 || len(ls) <= room {
		return ls
	}
	start := min(max(0, offset), len(ls)-room+1)
	end := start + room - 1
	out := append([]string(nil), ls[start:end]...)
	if n := len(ls) - end; n > 0 {
		out = append(out, " "+ctx.Theme.Muted.Render(moreMark(ctx, n)))
	}
	return out
}

// scrollBy moves a scroll offset by the arrow keys and the wheel.
func scrollBy(offset int, msg tea.Msg) int {
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		switch m.String() {
		case "down", "pgdown":
			return offset + 1
		case "up", "pgup":
			return max(0, offset-1)
		}
	case tea.MouseWheelMsg:
		if m.Button == tea.MouseWheelDown {
			return offset + 1
		}
		return max(0, offset-1)
	}
	return offset
}

// keyHints draws an inline row of "[key] label" hints.
func keyHints(ctx uictx.Context, pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, ctx.KeyHint(pairs[i], pairs[i+1]))
	}
	return " " + strings.Join(parts, "  ")
}

// --- errors in plain words ---

// explained is an error as a person reads it: what happened, why, and what
// to do, in numbered steps.
type explained struct {
	title string
	why   string
	steps []string
}

// explain turns an engine error into plain words. The error's own text is
// already plain (the engine writes it for people), so an unknown error is
// shown as it is.
func explain(err error) explained {
	msg := plainMessage(err)
	switch {
	case errors.Is(err, accounts.ErrLocked):
		return explained{"Another Devpit window is changing accounts", msg, []string{
			"Finish or close Accounts in the other Devpit window.", "Come back here and try again.",
		}}
	case errors.Is(err, accounts.ErrStalePreview), errors.Is(err, claudeshare.ErrStalePlan):
		return explained{"The accounts changed since the preview", "Something changed the accounts after the preview was shown, so nothing was changed.", []string{
			"Go back and look at the new preview.", "Say yes again if it is still what you want.",
		}}
	case errors.Is(err, accounts.ErrChangedByHand):
		return explained{"A file was changed by hand", msg, []string{
			"Open the file named above and put back what Devpit wrote, or keep your edit.",
			"Devpit only takes back lines it wrote itself, so it stopped instead of guessing.",
			"Run devpit accounts verify to see where things stand.",
		}}
	case errors.Is(err, accounts.ErrNothingToUndo):
		return explained{"Nothing to undo", "There is no change from Devpit that can still be taken back.", nil}
	case isInUse(err):
		return explained{"Claude Code is using this account", msg, []string{
			"Close every Claude Code window that uses this account.", "Press Enter to try again. Nothing was changed.",
		}}
	case isLinkRefused(err):
		return explained{"Windows will not make links here", msg, []string{
			"Press c to copy the items instead. You see the new preview first.",
		}}
	case errors.Is(err, accounts.ErrSignInCancelled):
		why := msg
		if why != "" {
			why = upperFirst(why) + ". "
		}
		return explained{"Signing in did not finish", why + "Nothing was saved and nothing was left behind.", []string{
			"Press Enter to try again, or Esc to go back.",
		}}
	case errors.Is(err, accounts.ErrNotFoundTool):
		return explained{"The tool is not installed", msg, []string{
			"Install it (Install & Update can do that), then open Accounts again.",
		}}
	case errors.Is(err, accounts.ErrTooOld):
		return explained{"The tool is too old for this", msg, []string{"Update it (Install & Update), then try again."}}
	case errors.Is(err, accounts.ErrNotSupported):
		return explained{"Devpit does not do this for this tool", msg, nil}
	}
	return explained{"That did not work", upperFirst(msg), nil}
}

// plainMessage is an error's text without the engine's sentinel phrases
// repeated at its start or end ("…: sign-in did not finish, so nothing was
// saved"), since the title already says them.
func plainMessage(err error) string {
	msg := accounts.Scrub(oneLine(err.Error()))
	for _, s := range []error{
		accounts.ErrSignInCancelled, accounts.ErrChangedByHand, accounts.ErrStalePreview, accounts.ErrNotSupported,
		accounts.ErrNotFoundTool, accounts.ErrTooOld, accounts.ErrLocked, accounts.ErrNothingToUndo,
	} {
		t := s.Error()
		msg = strings.TrimPrefix(msg, t+": ")
		msg = strings.TrimSuffix(msg, ": "+t)
	}
	return strings.TrimSpace(msg)
}

// upperFirst starts a sentence with a capital letter.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// isInUse reports a folder another program holds open. The engine's error
// reaches a screen scrubbed, as text, so the words are what is matched.
func isInUse(err error) bool {
	var inUse *claudeshare.InUseError
	return errors.As(err, &inUse) || (err != nil && strings.Contains(err.Error(), "most likely by a running Claude Code session"))
}

// isLinkRefused reports that Windows would not make a link in the account
// folder, so the setup can be copied instead.
func isLinkRefused(err error) bool {
	var link *claudeshare.LinkError
	return errors.As(err, &link) || (err != nil && strings.Contains(err.Error(), "links cannot be made in this account folder"))
}

// errorView draws an explained error.
func errorView(ctx uictx.Context, err error) string { return errorViewAs(ctx, err, "") }

// errorViewAs is errorView with the title given when the error itself is
// not one the screens know ("The change did not finish").
func errorViewAs(ctx uictx.Context, err error, title string) string {
	th := ctx.Theme
	e := explain(err)
	if title != "" && e.title == "That did not work" {
		e.title = title
	}
	lines := []string{" " + th.Danger.Render(ctx.Icons.Fail+" "+e.title), ""}
	lines = append(lines, wrap(ctx, th.Base, e.why, 1, 0)...)
	if len(e.steps) > 0 {
		lines = append(lines, "")
		for i, s := range e.steps {
			lines = append(lines, wrap(ctx, th.Base, strconv.Itoa(i+1)+". "+s, 1, 3)...)
		}
	}
	return strings.Join(lines, "\n")
}

// oneLine flattens text to a single line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

// --- inputs ---

// newInput returns a text field.
func newInput(placeholder string, limit int) textinput.Model {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.Placeholder = placeholder
	ti.CharLimit = limit
	ti.SetWidth(40)
	return ti
}

// styleInput puts the theme on a field: accent prompt, muted placeholder.
func styleInput(ti textinput.Model, ctx uictx.Context) textinput.Model {
	th := ctx.Theme
	state := textinput.StyleState{Text: th.Base, Placeholder: th.Muted, Suggestion: th.Muted, Prompt: th.Accent}
	st := ti.Styles()
	st.Focused, st.Blurred = state, state
	st.Cursor.Color = th.Palette.Accent
	st.Cursor.Blink = false
	ti.SetStyles(st)
	if ctx.Icons.Tier == icons.TierASCII {
		ti.Prompt = "> "
	}
	ti.SetWidth(max(10, min(60, ctx.Width-8)))
	return ti
}

// card draws text in the house card, never wider than the terminal.
func card(ctx uictx.Context, body string) string {
	st := ctx.Theme.CardFor(ctx.Icons.Tier == icons.TierASCII)
	if ctx.Width > 6 && lipgloss.Width(body)+4 > ctx.Width {
		st = st.Width(ctx.Width - 2)
	}
	return st.Render(body)
}

// plural picks one or many; many may hold a %d for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	if strings.Contains(many, "%d") {
		return strings.Replace(many, "%d", strconv.Itoa(n), 1)
	}
	return many
}
