package extract

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	extractengine "github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/tools/catalog"
	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/activity"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/filepicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Messages the screen sends itself. Every result of work carries the
// generation it was started in; onX drops one that is no longer current.
type (
	// initMsg is the first message, which brings the render context.
	initMsg struct{}
	// spinMsg advances the spinner.
	spinMsg struct{}
	// rowsMsg carries the archives the picker lists.
	rowsMsg struct{ rows []filepicker.Row }
	// preparedMsg says what a picked archive is: refused, unfinished, or
	// ready for its check.
	preparedMsg struct {
		gen        uint64
		set        archive.Set
		err        error
		outDir     string
		mode       extractengine.Mode
		unfinished bool
		started    time.Time
	}
	// toolMsg is the result of looking for hatchzip, and for Scoop when
	// hatchzip is missing or too old.
	toolMsg struct {
		gen     uint64
		found   tools.Tool
		tool    extractengine.Tool
		err     error
		scoop   bool
		steps   []managers.Step
		planErr error
	}
	// checkedMsg is one `hatchzip check`.
	checkedMsg struct {
		gen uint64
		req extractengine.Request
		res extractengine.CheckResult
		err error
	}
	// startedMsg says the extraction started, or why it did not.
	startedMsg struct {
		gen uint64
		err error
	}
	// progressMsg is the newest progress of the run.
	progressMsg struct {
		gen uint64
		p   extractengine.Progress
	}
	// outcomeMsg is the end of the run.
	outcomeMsg struct {
		gen    uint64
		out    extractengine.Outcome
		free   uint64
		freeOK bool
	}
	// stepDoneMsg is the end of one install step.
	stepDoneMsg struct {
		gen     uint64
		i       int
		ok      bool
		res     tools.StepResult
		stopped bool
	}
	// restoredMsg is the end of a restore.
	restoredMsg struct {
		gen uint64
		out extractengine.Outcome
		err error
	}
)

// Menu row identifiers.
const (
	rowInstall = "install"
	rowNotNow  = "notnow"
	rowResume  = "resume"
	rowRestore = "restore"
	rowLeave   = "leave"
)

// toolKind is what is wrong with hatchzip.
type toolKind int

const (
	toolMissing toolKind = iota
	toolOld
)

// toolInfo is what the tool card shows.
type toolInfo struct {
	kind    toolKind
	version string
	// scoop says Scoop is on this PC, so Devpit can install hatchzip itself.
	scoop bool
	// steps are the exact commands an install would run.
	steps []managers.Step
	// note is a line about an install that just ended.
	note string
}

// installRun is one run of the install steps.
type installRun struct {
	steps     []managers.Step
	rows      []activity.Row
	idx       int
	log       []string
	gate      activity.SkipGate
	skipped   bool
	stepStart time.Time
	lastOut   time.Time
	// reason is the one line under the rows when the run did not finish.
	reason string
}

// maxFound is how many found archives the picker lists under the unfinished
// ones.
const maxFound = 8

// onInit starts what the opening state needs: the archive search for the
// picker, or a look at the archive the screen was opened on.
func (m Model) onInit(ctx uictx.Context) (Model, tea.Cmd) {
	m.projects = ctx.Config.DefaultProjectsFolder
	switch {
	case m.openPath != "":
		return m.open(m.openPath)
	case m.limited:
		return m, nil
	default:
		return m, m.rowsCmd(ctx.Config)
	}
}

// rowsCmd looks for the archives the picker lists, off the update loop:
// Devpit's unfinished ones, then the big ones in Downloads and the folders
// the user works in.
func (m Model) rowsCmd(cfg config.Config) tea.Cmd {
	eng, find, ctx := m.records(), m.find, m.life.ctx
	dirs := searchDirs(cfg)
	return func() tea.Msg {
		return rowsMsg{rows: buildRows(eng.Pending(ctx), find(ctx, dirs))}
	}
}

// searchDirs is where the picker looks: Downloads, the default projects
// folder and the folders scanned recently.
func searchDirs(cfg config.Config) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[strings.ToLower(p)] {
			return
		}
		seen[strings.ToLower(p)] = true
		out = append(out, p)
	}
	add(downloadsFolder())
	add(cfg.DefaultProjectsFolder)
	for _, p := range cfg.RecentFolders {
		add(p)
	}
	return out
}

// buildRows orders the picker's rows: unfinished extractions first, then the
// found archives, newest first, at most [maxFound] of them.
func buildRows(pending []extractengine.Unfinished, found []extractengine.Found) []filepicker.Row {
	seen := map[string]bool{}
	var rows []filepicker.Row
	for _, u := range pending {
		k := strings.ToLower(u.Set.First)
		if seen[k] {
			continue
		}
		seen[k] = true
		rows = append(rows, filepicker.Row{Set: u.Set, Unfinished: true, Started: u.Started})
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].Mod.After(found[j].Mod) })
	for _, f := range found {
		if k := strings.ToLower(f.Set.First); f.Unfinished && !seen[k] {
			seen[k] = true
			rows = append(rows, filepicker.Row{Set: f.Set, Unfinished: true})
		}
	}
	n := 0
	for _, f := range found {
		k := strings.ToLower(f.Set.First)
		if seen[k] || n == maxFound {
			continue
		}
		seen[k] = true
		n++
		rows = append(rows, filepicker.Row{Set: f.Set})
	}
	return rows
}

// rowsOf turns the sets a caller handed over into picker rows.
func rowsOf(sets []archive.Set) []filepicker.Row {
	rows := make([]filepicker.Row, 0, len(sets))
	for _, s := range sets {
		rows = append(rows, filepicker.Row{Set: s})
	}
	return rows
}

// open looks at an archive the screen was opened on, with the same checks a
// typed path gets.
func (m Model) open(path string) (Model, tea.Cmd) {
	m.gen++
	m.state = stateChecking
	m.name = filepicker.Base(path)
	gen, identify := m.gen, m.identify
	look := m.lookup()
	cmd := func() tea.Msg {
		set, err := identify(path)
		if err != nil {
			return preparedMsg{gen: gen, err: err}
		}
		return look(gen, set)
	}
	spin := m.spin()
	return m, tea.Batch(cmd, spin)
}

// prepare looks at an archive the picker chose: is an extraction of it
// unfinished, and where would a fresh one go.
func (m Model) prepare(set archive.Set) (Model, tea.Cmd) {
	m.gen++
	m.state = stateChecking
	m.waitText = ""
	m.name = filepicker.Base(set.First)
	gen, look := m.gen, m.lookup()
	spin := m.spin()
	return m, tea.Batch(func() tea.Msg { return look(gen, set) }, spin)
}

// lookup builds the function that answers prepare. An archive is unfinished
// when Devpit has a record of it whose journal still exists, or when a
// journal lies beside it with no record at all.
func (m Model) lookup() func(gen uint64, set archive.Set) tea.Msg {
	eng, fsys, outDir, ctx := m.records(), m.fsys, m.outDir, m.life.ctx
	return func(gen uint64, set archive.Set) tea.Msg {
		for _, u := range eng.Pending(ctx) {
			if strings.EqualFold(u.Set.First, set.First) {
				return preparedMsg{gen: gen, set: set, outDir: u.OutDir, mode: u.Mode, unfinished: true, started: u.Started}
			}
		}
		out := outDir(set)
		_, err := fsys.Lstat(archive.JournalPath(set.First))
		return preparedMsg{gen: gen, set: set, outDir: out, mode: extractengine.ModeInPlace, unfinished: err == nil}
	}
}

// onPrepared takes the archive on: back to the picker with the reason when
// it was refused, to Resume / Restore when it is unfinished, on to the check
// otherwise.
func (m Model) onPrepared(msg preparedMsg, ctx uictx.Context) (Model, tea.Cmd) {
	if msg.gen != m.gen || m.state != stateChecking {
		return m, nil
	}
	if msg.err != nil {
		m.state = statePick
		m.picker = m.picker.SetRefusal(filepicker.Refusal(msg.err))
		if m.limited {
			return m, nil
		}
		// The screen was opened on this archive, so the picker has not
		// looked for any others yet.
		m.picker = m.picker.SetLoading(true)
		return m, m.rowsCmd(ctx.Config)
	}
	m.name = filepicker.Base(msg.set.First)
	m.req = extractengine.Request{Set: msg.set, OutDir: msg.outDir, Mode: msg.mode, Password: m.secret}
	m.resuming = msg.unfinished
	m.started = msg.started
	m.modeChosen, m.autoKeep = false, false
	m.ticket = nil
	if msg.unfinished {
		m.state = stateUnfinished
		m.menu = menu.New(unfinishedRows())
		return m, nil
	}
	return m.proceed(actCheck)
}

// unfinishedRows is the Resume / Restore / Leave menu.
func unfinishedRows() []menu.Item {
	return []menu.Item{
		{ID: rowResume, Title: "Resume", Desc: "Check the space, then carry on where it stopped"},
		{ID: rowRestore, Title: "Restore the archive", Desc: "Rebuild a working archive from the files not extracted yet"},
		{ID: rowLeave, Title: "Leave it for now", Desc: "Change nothing; the Clean menu keeps reminding you"},
	}
}

// proceed does act once a usable hatchzip is known, looking for one first.
func (m Model) proceed(act action) (Model, tea.Cmd) {
	m.after = act
	if m.toolOK {
		return m.runAfter()
	}
	return m.detectTool()
}

// runAfter does what the screen was on its way to.
func (m Model) runAfter() (Model, tea.Cmd) {
	if m.after == actRestore {
		return m.startRestore()
	}
	return m.startCheck()
}

// detectTool looks for hatchzip off the update loop, and for Scoop when
// hatchzip cannot be used as it is.
func (m Model) detectTool() (Model, tea.Cmd) {
	m.gen++
	m.state = stateChecking
	gen, detect, scoop, ctx := m.gen, m.detect, m.scoop, m.life.ctx
	cmd := func() tea.Msg {
		found := detect(ctx)
		tool, err := extractengine.UseTool(found)
		msg := toolMsg{gen: gen, found: found, tool: tool, err: err}
		if err != nil {
			msg.scoop = scoop(ctx).Found
			if msg.scoop {
				msg.steps, msg.planErr = installSteps(errors.Is(err, extractengine.ErrToolTooOld))
			}
		}
		return msg
	}
	spin := m.spin()
	return m, tea.Batch(cmd, spin)
}

// installSteps is the exact commands that install hatchzip with Scoop, from
// the built-in catalog and Devpit's list of known buckets, or that update it.
func installSteps(update bool) ([]managers.Step, error) {
	scoop := managers.Scoop{}
	if update {
		return []managers.Step{
			{Argv: scoop.CheckCmds()[0], Label: "Refreshing Scoop's buckets"},
			{Argv: scoop.UpgradeCmd("hatchzip"), Label: "Updating hatchzip"},
		}, nil
	}
	apps, err := catalog.Load()
	if err != nil {
		return nil, err
	}
	for _, app := range apps {
		if app.Name == "hatchzip" {
			return managers.InstallPlan(scoop, app)
		}
	}
	return nil, errors.New("hatchzip is not in the catalog")
}

// onTool carries on when hatchzip is usable, and shows what is wrong when it
// is not.
func (m Model) onTool(msg toolMsg) (Model, tea.Cmd) {
	if msg.gen != m.gen || m.state != stateChecking {
		return m, nil
	}
	m.waitText = ""
	if msg.err == nil {
		m.toolOK = true
		if !m.injected {
			m.eng = extractengine.New(msg.tool, extractengine.Real())
		}
		return m.runAfter()
	}
	note := ""
	if m.inst.steps != nil && m.inst.reason == "" && len(m.inst.rows) > 0 {
		// The install just finished cleanly, and hatchzip is still not found.
		note = "The install finished, but Devpit still cannot find a usable hatchzip. Restart Devpit, or press r to check again."
	}
	m.tool = toolInfo{
		kind:    toolMissing,
		version: msg.found.Version,
		scoop:   msg.scoop && msg.planErr == nil && len(msg.steps) > 0,
		steps:   msg.steps,
		note:    note,
	}
	if errors.Is(msg.err, extractengine.ErrToolTooOld) {
		m.tool.kind = toolOld
	}
	m.inst = installRun{}
	m.state = stateTool
	m.menu = menu.New(m.toolRows())
	return m, nil
}

// toolRows is the menu of the tool card. Without Scoop there is nothing to
// choose.
func (m Model) toolRows() []menu.Item {
	if !m.tool.scoop {
		return nil
	}
	first := menu.Item{
		ID:    rowInstall,
		Title: "Install hatchzip with Scoop",
		Desc:  "Adds the xevrion bucket, then installs hatchzip. No admin needed.",
	}
	if m.tool.kind == toolOld {
		first = menu.Item{
			ID:    rowInstall,
			Title: "Update hatchzip with Scoop",
			Desc:  "Refreshes Scoop's buckets, then updates hatchzip. No admin needed.",
		}
	}
	return []menu.Item{first, {ID: rowNotNow, Title: "Not now", Desc: "Go back"}}
}

// startCheck runs `hatchzip check` for the current request.
func (m Model) startCheck() (Model, tea.Cmd) {
	if m.eng == nil {
		return m.detectTool()
	}
	m.gen++
	m.state = stateChecking
	m.details = false
	m.ticket = nil
	m.pausing, m.autoResumes = false, 0
	m.req.Password = m.secret
	gen, eng, req, ctx := m.gen, m.eng, m.req, m.life.ctx
	cmd := func() tea.Msg {
		res, err := eng.Check(ctx, req)
		return checkedMsg{gen: gen, req: req, res: res, err: err}
	}
	spin := m.spin()
	return m, tea.Batch(cmd, spin)
}

// onChecked shows what the check found. Only a check that passed leaves a
// ticket, and only a ticket can start a run.
func (m Model) onChecked(msg checkedMsg) (Model, tea.Cmd) {
	if msg.gen != m.gen || m.state != stateChecking {
		return m, nil
	}
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) {
			return m, nil
		}
		m.check = extractengine.CheckResult{Verdict: extractengine.VerdictFailed, Exit: -1, Error: msg.err.Error()}
		m.state = stateCheck
		return m, nil
	}
	res := msg.res
	m.check = res
	if res.Plan.Resuming {
		m.resuming = true
	}
	switch res.Verdict {
	case extractengine.VerdictNeedPassword:
		return m.askPassword(false)
	case extractengine.VerdictWrongPassword:
		return m.askPassword(true)
	case extractengine.VerdictFits:
		// When there is room to keep the archive, keeping it is the safer
		// default: nothing is deleted. The request changes, so it needs a
		// check of its own.
		if m.req.Mode == extractengine.ModeInPlace && res.Plan.KeepArchiveFits && m.canSwitchMode() && !m.modeChosen && !m.autoKeep {
			m.autoKeep = true
			m.req.Mode = extractengine.ModeKeepArchive
			return m.startCheck()
		}
		m.ticket = res.Ticket
	default:
		if m.autoKeep && !m.modeChosen && m.req.Mode == extractengine.ModeKeepArchive {
			// Keeping the archive was Devpit's own idea and it does not
			// fit after all; in place did a moment ago.
			m.req.Mode = extractengine.ModeInPlace
			return m.startCheck()
		}
	}
	m.state = stateCheck
	return m, nil
}

// canSwitchMode reports whether the user may choose between in place and
// keeping the archive. A resumed extraction carries on the way it began.
func (m Model) canSwitchMode() bool { return !m.resuming }

// switchMode checks again in the other mode.
func (m Model) switchMode(mode extractengine.Mode) (Model, tea.Cmd) {
	if m.req.Mode == mode {
		return m, nil
	}
	m.req.Mode = mode
	m.modeChosen = true
	return m.startCheck()
}

// askPassword shows the password field. It is only ever reached after
// hatchzip itself said a password is needed.
func (m Model) askPassword(wrong bool) (Model, tea.Cmd) {
	m.secret = extractengine.Secret{}
	m.req.Password = extractengine.Secret{}
	m.wrongPass = wrong
	m.pass = newPasswordInput()
	m.state = statePassword
	focus := m.pass.Focus()
	return m, focus
}

// newPasswordInput returns the masked password field.
func newPasswordInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.EchoMode = textinput.EchoPassword
	ti.SetWidth(40)
	return ti
}

// submitPassword takes the typed password into memory, empties the field and
// checks again with it.
func (m Model) submitPassword() (Model, tea.Cmd) {
	v := m.pass.Value()
	if v == "" {
		return m, nil
	}
	m.secret = extractengine.NewSecret(v)
	m.pass.SetValue("")
	m.pass.Blur()
	return m.startCheck()
}

// askRun shows the run confirmation. It is the only way into stateConfirm,
// and it needs the ticket of a passed check.
func (m Model) askRun() (Model, tea.Cmd) {
	if m.state != stateCheck || m.ticket == nil || m.check.Verdict != extractengine.VerdictFits {
		return m, nil
	}
	q, d := m.runQuestion()
	m.dialog = confirm.New(runDialogID, q, d)
	m.state = stateConfirm
	return m, nil
}

// onMenu acts on a row of the tool card or the Resume / Restore menu.
func (m Model) onMenu(id string) (Model, tea.Cmd) {
	switch m.state {
	case stateTool:
		switch id {
		case rowInstall:
			if !m.tool.scoop {
				return m, nil
			}
			q, d := m.installQuestion()
			m.dialog = confirm.New(installDialogID, q, d)
			m.state = stateToolConfirm
		case rowNotNow:
			return m.back()
		}
	case stateUnfinished:
		switch id {
		case rowResume:
			return m.proceed(actCheck)
		case rowRestore:
			q, d := m.restoreQuestion()
			m.dialog = confirm.New(restoreDialogID, q, d)
			m.state = stateRestoreConfirm
		case rowLeave:
			// Devpit's record stays and hatchzip's side files are never
			// touched: the Clean menu goes on saying so.
			return m, uictx.Pop()
		}
	}
	return m, nil
}

// back returns to the picker, or leaves the screen when there is none to
// return to.
func (m Model) back() (Model, tea.Cmd) {
	if m.openPath != "" {
		return m, uictx.Pop()
	}
	m.state = statePick
	return m, nil
}

// onAnswer handles a confirmation. A No goes back where it came from with
// nothing changed; a Yes is the only thing that reaches beginExtract,
// beginInstall or the restore.
func (m Model) onAnswer(msg confirm.AnsweredMsg) (Model, tea.Cmd) {
	yes := msg.Answer == confirm.AnswerYes
	switch {
	case msg.ID == runDialogID && m.state == stateConfirm:
		if !yes {
			m.state = stateCheck
			m.dialog = m.dialog.Reset()
			return m, uictx.Status("", "Nothing was extracted.")
		}
		return m.beginExtract(msg)

	case msg.ID == installDialogID && m.state == stateToolConfirm:
		if !yes {
			m.state = stateTool
			m.dialog = m.dialog.Reset()
			return m, uictx.Status("", "Nothing was installed.")
		}
		return m.beginInstall(msg)

	case msg.ID == restoreDialogID && m.state == stateRestoreConfirm:
		if !yes {
			m.state = stateUnfinished
			m.dialog = m.dialog.Reset()
			return m, uictx.Status("", "Nothing was changed.")
		}
		m.restoreOK = true
		return m.proceed(actRestore)
	}
	return m, nil
}

// beginExtract is the only function that starts an extraction, and it is
// safety rule 76 in code.
//
// It refuses unless the answer it was handed is a Yes for this screen's own
// run dialog and the check that just passed left a ticket. Every other path
// through the screen ends somewhere else, because nothing else calls
// [Extractor.Start]. The ticket is used once: a second Yes finds none.
func (m Model) beginExtract(answer confirm.AnsweredMsg) (Model, tea.Cmd) {
	if answer.ID != runDialogID || answer.Answer != confirm.AnswerYes || m.state != stateConfirm {
		return m, nil
	}
	if m.ticket == nil || m.eng == nil {
		m.state = stateCheck
		return m, uictx.Status("warning", "The space check has to pass first.")
	}
	ticket := m.ticket
	m.ticket = nil

	m.gen++
	m.state = stateRunning
	m.pausing, m.autoResumes = false, 0
	m.out = extractengine.Outcome{}
	m.prog = extractengine.Progress{
		Phase:      extractengine.PhaseStarting,
		TotalBytes: m.check.Plan.TotalBytes,
		TotalItems: m.check.Plan.Entries,
	}
	if m.check.Plan.Resuming && m.check.Plan.LeftBytes <= m.check.Plan.TotalBytes {
		m.prog.DoneBytes = min(m.check.Plan.TotalBytes-m.check.Plan.LeftBytes, m.check.Plan.TotalBytes*99/100)
	}

	// The run's context is its own, not the screen's: ending it pauses
	// hatchzip, and only Esc, Stop and Ctrl+C may do that.
	op, eng, gen := m.op, m.eng, m.gen
	runCtx := op.begin(context.Background())
	feed := op.feed
	start := func() tea.Msg {
		job, err := eng.Start(runCtx, ticket, func(p extractengine.Progress) { op.push(feed, p) })
		if err != nil {
			return startedMsg{gen: gen, err: err}
		}
		op.setJob(job)
		return startedMsg{gen: gen}
	}
	spin := m.spin()
	return m, tea.Batch(start, spin)
}

// onStarted begins listening to the run, or reports that it never started.
func (m Model) onStarted(msg startedMsg) (Model, tea.Cmd) {
	if msg.gen != m.gen || m.state != stateRunning {
		return m, nil
	}
	if msg.err != nil {
		m.out = extractengine.Outcome{Kind: extractengine.KindFailed, Exit: -1, Error: msg.err.Error()}
		m.state = stateFailed
		return m, nil
	}
	return m, tea.Batch(m.waitProgress(), m.waitOutcome())
}

// waitProgress reads the newest progress, blocking on its own goroutine the
// way every tea.Cmd does. It returns nothing once the run has ended.
func (m Model) waitProgress() tea.Cmd {
	gen := m.gen
	m.op.mu.Lock()
	feed, finished := m.op.feed, m.op.finished
	m.op.mu.Unlock()
	return func() tea.Msg {
		select {
		case p := <-feed:
			return progressMsg{gen: gen, p: p}
		case <-finished:
			return nil
		}
	}
}

// waitOutcome waits for the run to end and reads the free space it left.
func (m Model) waitOutcome() tea.Cmd {
	gen, free, out := m.gen, m.free, m.req.OutDir
	m.op.mu.Lock()
	job, finished := m.op.job, m.op.finished
	m.op.mu.Unlock()
	if job == nil {
		return nil
	}
	return func() tea.Msg {
		o := <-job.Done()
		close(finished)
		msg := outcomeMsg{gen: gen, out: o}
		if o.Kind == extractengine.KindDone {
			msg.free, msg.freeOK = free(out)
		}
		return msg
	}
}

// onProgress folds one progress update into the bar and asks for the next.
// The bar never goes backwards, whatever arrives.
func (m Model) onProgress(msg progressMsg) (Model, tea.Cmd) {
	if msg.gen != m.gen || m.state != stateRunning {
		return m, nil
	}
	p := msg.p
	p.DoneBytes = max(p.DoneBytes, m.prog.DoneBytes)
	p.DoneItems = max(p.DoneItems, m.prog.DoneItems)
	if p.TotalBytes <= 0 {
		p.TotalBytes = m.prog.TotalBytes
	}
	if p.TotalItems <= 0 {
		p.TotalItems = m.prog.TotalItems
	}
	p.AutoResumes = max(p.AutoResumes, m.prog.AutoResumes)
	m.autoResumes = p.AutoResumes
	m.prog = p
	return m, m.waitProgress()
}

// onOutcome shows how the run ended.
func (m Model) onOutcome(msg outcomeMsg) (Model, tea.Cmd) {
	if msg.gen != m.gen || m.state != stateRunning {
		return m, nil
	}
	m.out = msg.out
	m.freeAfter, m.freeKnown = msg.free, msg.freeOK
	m.pausing = false
	// The outcome's count is the true one: a resume that was announced and
	// then paused before it ran is not in it.
	m.autoResumes = msg.out.AutoResumes
	switch msg.out.Kind {
	case extractengine.KindDone:
		// The password has done its job: drop it.
		m.secret = extractengine.Secret{}
		m.req.Password = extractengine.Secret{}
		m.state = stateDone
	case extractengine.KindPaused, extractengine.KindStopped:
		m.resuming = msg.out.CanResume || m.resuming
		m.state = statePaused
	case extractengine.KindNeedPassword:
		return m.askPassword(false)
	case extractengine.KindWrongPassword:
		return m.askPassword(true)
	default:
		if msg.out.CanResume {
			m.resuming = true
		}
		m.state = stateFailed
	}
	return m, nil
}

// pause asks a running extraction to pause. hatchzip finishes the part it is
// on; the engine escalates by itself if it does not.
func (m Model) pause() (Model, tea.Cmd) {
	if m.state != stateRunning || m.pausing {
		return m, nil
	}
	m.pausing = true
	m.op.stop()
	return m, uictx.Status("warning", "Pausing: hatchzip is finishing the part it is on…")
}

// beginInstall is the only function that runs an install command, and it
// refuses anything but a Yes for this screen's install dialog (rule 88).
func (m Model) beginInstall(answer confirm.AnsweredMsg) (Model, tea.Cmd) {
	if answer.ID != installDialogID || answer.Answer != confirm.AnswerYes || m.state != stateToolConfirm {
		return m, nil
	}
	if !m.tool.scoop || len(m.tool.steps) == 0 {
		m.state = stateTool
		return m, nil
	}
	m.gen++
	m.state = stateInstalling
	m.details = false
	rows := make([]activity.Row, len(m.tool.steps))
	for i, s := range m.tool.steps {
		rows[i] = activity.Row{Label: s.Label, State: activity.Queued, Percent: -1}
	}
	m.inst = installRun{steps: m.tool.steps, rows: rows}
	cmd := m.runStepCmd(0)
	spin := m.spin()
	return m, tea.Batch(cmd, spin)
}

// runStepCmd runs install step i off the update loop.
func (m *Model) runStepCmd(i int) tea.Cmd {
	now := m.now()
	m.inst.idx = i
	m.inst.rows[i].State = activity.Running
	m.inst.stepStart, m.inst.lastOut = now, now
	m.inst.gate = activity.SkipGate{}

	step, run, gen, op := m.inst.steps[i], m.runStep, m.gen, m.op
	ctx := op.begin(m.life.ctx)
	lines := op.lines
	return func() tea.Msg {
		res := run(ctx, step.Argv, tools.DefaultStepTimeout, func(line string) { say(lines, line) })
		stopped := ctx.Err() != nil
		ok := res.OK
		if !ok && !stopped && step.OKIfLine != nil && res.ExitCode >= 0 && step.OKIfLine(res.ExitCode, res.LastLines) {
			ok = true
		}
		return stepDoneMsg{gen: gen, i: i, ok: ok, res: res, stopped: stopped}
	}
}

// drainInstall folds what the running step has printed into its row and the
// log, and refreshes its elapsed time and the stuck hint.
func (m Model) drainInstall() Model {
	if m.inst.idx >= len(m.inst.rows) {
		return m
	}
	now := m.now()
	row := &m.inst.rows[m.inst.idx]
	for {
		select {
		case line := <-m.op.lines:
			m.inst.log = append(m.inst.log, line)
			row.Detail = line
			m.inst.lastOut = now
			continue
		default:
		}
		break
	}
	row.Elapsed = now.Sub(m.inst.stepStart)
	row.Stuck = 0
	if silent := now.Sub(m.inst.lastOut); silent >= activity.StuckAfter {
		row.Stuck = silent
	}
	return m
}

// onStepDone moves to the next install step, or ends the run: on to the
// check when every step worked, to the reason when one did not.
func (m Model) onStepDone(msg stepDoneMsg) (Model, tea.Cmd) {
	if msg.gen != m.gen || m.state != stateInstalling || msg.i != m.inst.idx {
		return m, nil
	}
	m = m.drainInstall()
	for _, l := range msg.res.LastLines {
		if len(m.inst.log) == 0 || !containsLine(m.inst.log, l) {
			m.inst.log = append(m.inst.log, l)
		}
	}
	row := &m.inst.rows[msg.i]
	row.Stuck = 0
	step := m.inst.steps[msg.i]

	if msg.ok {
		row.State, row.Detail = activity.Done, ""
		if !msg.res.OK {
			row.Detail = "already there"
		}
		if next := msg.i + 1; next < len(m.inst.steps) {
			cmd := m.runStepCmd(next)
			return m, cmd
		}
		// Installed. Look for hatchzip again and carry on by itself.
		m.toolOK = false
		m.waitText = "Looking for hatchzip…"
		return m.detectTool()
	}

	switch {
	case m.inst.skipped:
		row.State, row.Detail = activity.Skipped, "skipped by you"
		m.inst.reason = "You skipped \"" + step.Label + "\", so hatchzip was not installed."
	case msg.stopped:
		row.State, row.Detail = activity.Skipped, "stopped"
		m.inst.reason = "The install was stopped, so hatchzip was not installed."
	default:
		row.State = activity.Failed
		row.Detail = lastLine(msg.res.LastLines)
		if row.Detail == "" {
			row.Detail = "it did not finish"
		}
		m.inst.reason = "\"" + step.Label + "\" failed, so nothing after it was run."
	}
	for i := msg.i + 1; i < len(m.inst.rows); i++ {
		m.inst.rows[i].State, m.inst.rows[i].Detail = activity.Skipped, "not run"
	}
	m.state = stateInstallFailed
	return m, nil
}

// skipStep is the skip key during an install: the first press arms it and
// says so, the second stops the step in flight. Both steps are needed, so a
// skipped step ends the install.
func (m Model) skipStep() (Model, tea.Cmd) {
	if m.state != stateInstalling || m.inst.idx >= len(m.inst.steps) {
		return m, nil
	}
	gate, fire := m.inst.gate.Press(m.inst.idx, m.now())
	m.inst.gate = gate
	label := m.inst.steps[m.inst.idx].Label
	if !fire {
		return m, uictx.Status("warning", "Press s again to skip \""+label+"\". hatchzip will not be installed.")
	}
	m.inst.skipped = true
	m.op.stop()
	return m, uictx.Status("warning", "Skipping…")
}

// startRestore runs `hatchzip restore`. It needs the Yes of the restore
// dialog, which onAnswer records in restoreOK and this consumes.
func (m Model) startRestore() (Model, tea.Cmd) {
	if !m.restoreOK {
		m.state = stateUnfinished
		return m, nil
	}
	if m.eng == nil {
		return m.detectTool()
	}
	m.restoreOK = false
	m.gen++
	m.state = stateRestoring
	m.details = false
	m.restoreStart = m.now()
	m.restoreTook, m.restoreLine, m.restoreErr = 0, "", ""
	m.out = extractengine.Outcome{}

	gen, eng, req, op := m.gen, m.eng, m.req, m.op
	ctx := op.begin(m.life.ctx)
	lines := op.lines
	cmd := func() tea.Msg {
		out, err := eng.Restore(ctx, req, func(line string) { say(lines, line) })
		if err == nil && out.Kind == extractengine.KindDone {
			// The archive is whole again, so Devpit's record of the
			// unfinished extraction goes. Nothing else is touched.
			_ = eng.Forget(req.Set.First)
		}
		return restoredMsg{gen: gen, out: out, err: err}
	}
	spin := m.spin()
	return m, tea.Batch(cmd, spin)
}

// drainRestore keeps the newest line the restore printed and its elapsed
// time.
func (m Model) drainRestore() Model {
	for {
		select {
		case line := <-m.op.lines:
			m.restoreLine = line
			continue
		default:
		}
		break
	}
	m.restoreTook = m.now().Sub(m.restoreStart)
	return m
}

// onRestored shows how the restore ended.
func (m Model) onRestored(msg restoredMsg) (Model, tea.Cmd) {
	if msg.gen != m.gen || m.state != stateRestoring {
		return m, nil
	}
	m = m.drainRestore()
	m.out = msg.out
	switch {
	case msg.err != nil && errors.Is(msg.err, context.Canceled):
		m.restoreErr = "The restore was stopped. The unfinished extraction is as it was."
	case msg.err != nil:
		m.restoreErr = "hatchzip could not be started: " + msg.err.Error()
	case msg.out.Kind != extractengine.KindDone:
		m.restoreErr = "hatchzip could not restore the archive."
		if msg.out.Error != "" {
			m.restoreErr = "hatchzip could not restore the archive: " + msg.out.Error
		}
	}
	m.state = stateRestored
	return m, nil
}

// lastLine is the last non-empty line.
func lastLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

// containsLine reports whether lines already holds l.
func containsLine(lines []string, l string) bool {
	for _, x := range lines {
		if x == l {
			return true
		}
	}
	return false
}
