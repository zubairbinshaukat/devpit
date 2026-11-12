package extract

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	extractengine "github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/activity"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/filepicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// sep is the house separator between facts on one line.
const sep = "  ·  "

// cardInnerMax is the widest a card's text runs, so a card on a wide
// terminal stays a card and does not become a banner.
const cardInnerMax = 72

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string {
	if ctx.Width < uictx.MinWidth || ctx.Height < uictx.MinHeight {
		return ctx.Theme.Notice.Render(fmt.Sprintf(
			"Extract needs a bigger window.\nNow: %d×%d   Needs: %d×%d",
			ctx.Width, ctx.Height, uictx.MinWidth, uictx.MinHeight,
		))
	}
	if m.details {
		return m.detailsView(ctx)
	}
	switch m.state {
	case stateChecking:
		return m.waitView(ctx)
	case stateTool:
		return m.toolView(ctx)
	case stateToolConfirm, stateConfirm, stateRestoreConfirm:
		return m.dialog.View(ctx)
	case stateInstalling:
		return m.installingView(ctx)
	case stateInstallFailed:
		return m.installFailedView(ctx)
	case stateCheck:
		return m.checkView(ctx)
	case stateFolder:
		return m.folderView(ctx)
	case stateUnfinished:
		return m.unfinishedView(ctx)
	case statePassword:
		return m.passwordView(ctx)
	case stateRunning:
		return m.runningView(ctx)
	case stateDone:
		return m.doneView(ctx)
	case statePaused:
		return m.pausedView(ctx)
	case stateFailed:
		return m.failedView(ctx)
	case stateRestoring:
		return m.restoringView(ctx)
	case stateRestored:
		return m.restoredView(ctx)
	default:
		return m.picker.View(ctx)
	}
}

// waitView is the one line shown while Devpit looks at the file, looks for
// hatchzip or waits for `hatchzip check`.
func (m Model) waitView(ctx uictx.Context) string {
	text := m.waitText
	if text == "" {
		text = "Checking how much space " + m.name + " needs…"
	}
	return ctx.Theme.Muted.Render(ctx.Truncate(ctx.SpinnerFrame(m.frame) + " " + text))
}

// toolLead is what the tool card says above its menu.
func (m Model) toolLead(ctx uictx.Context) string {
	th := ctx.Theme
	switch {
	case m.tool.kind == toolOld && m.tool.scoop:
		return para(ctx, th.Base, "hatchzip "+m.tool.version+" is installed. Devpit needs "+extractengine.MinVersion+" or newer.")
	case m.tool.kind == toolOld:
		return para(ctx, th.Base, "hatchzip "+m.tool.version+" is installed. Devpit needs "+extractengine.MinVersion+" or newer, and updates it with Scoop, which is not on this PC.")
	case m.tool.scoop:
		return para(ctx, th.Base, "Extracting in place uses hatchzip, a free open-source tool (MIT/Apache 2.0) by xevrion. It is not installed yet.")
	default:
		return para(ctx, th.Base, "hatchzip is not installed. Devpit installs it with Scoop, which is not on this PC either.")
	}
}

// toolView says hatchzip is missing or too old. With Scoop it offers the
// install and shows the exact commands; without, it says where to get both.
func (m Model) toolView(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(m.toolLead(ctx))
	b.WriteString("\n\n")
	if !m.tool.scoop {
		what := "Install Scoop from https://scoop.sh, or hatchzip from https://github.com/xevrion/hatchzip, then press r to check again."
		if m.tool.kind == toolOld {
			what = "Get the newest hatchzip from https://github.com/xevrion/hatchzip, then press r to check again."
		}
		b.WriteString(para(ctx, th.Muted, what))
		return b.String()
	}
	b.WriteString(m.menu.View(ctx))
	b.WriteString("\n\n")
	b.WriteString(th.Muted.Render("Devpit will run exactly:"))
	for _, s := range m.tool.steps {
		b.WriteString("\n")
		b.WriteString(th.Base.Render(ctx.Truncate("  " + strings.Join(s.Argv, " "))))
	}
	if m.tool.note != "" {
		b.WriteString("\n\n")
		b.WriteString(para(ctx, th.Warning, ctx.Icons.Warn+" "+m.tool.note))
	}
	return b.String()
}

// unfinishedLead is what the Resume / Restore step says above its menu.
func (m Model) unfinishedLead(ctx uictx.Context) string {
	th := ctx.Theme
	head := m.name + " has an unfinished extraction."
	if !m.started.IsZero() {
		head = m.name + " has an unfinished extraction, started " + filepicker.Ago(m.now().Sub(m.started)) + "."
	}
	where := pathLines("The files written so far are in", m.req.OutDir, ctx.Width, ascii(ctx))
	return para(ctx, th.Base, head) + "\n" + strings.Join(styled(th.Muted, where), "\n")
}

// unfinishedView offers Resume, Restore or leaving it.
func (m Model) unfinishedView(ctx uictx.Context) string {
	return m.unfinishedLead(ctx) + "\n\n" + m.menu.View(ctx)
}

// menuTop is the body row the menu of the tool card or the Resume / Restore
// step starts on, measured from what the view draws, or -1 without a menu.
func (m Model) menuTop(ctx uictx.Context) int {
	switch m.state {
	case stateTool:
		if !m.tool.scoop {
			return -1
		}
		return lipgloss.Height(m.toolLead(ctx)) + 1
	case stateUnfinished:
		return lipgloss.Height(m.unfinishedLead(ctx)) + 1
	}
	return -1
}

// installingView is one live row per install step.
func (m Model) installingView(ctx uictx.Context) string {
	th := ctx.Theme
	verb := "Installing"
	if m.tool.kind == toolOld {
		verb = "Updating"
	}
	var b strings.Builder
	b.WriteString(th.Title.Render(verb) + th.Muted.Render(" hatchzip with Scoop"))
	b.WriteString("\n\n")
	b.WriteString(activity.View(ctx, m.inst.rows, m.frame, ctx.Width, max(3, ctx.BodyHeight-6)))
	b.WriteString("\n\n")
	b.WriteString(para(ctx, th.Muted, "Devpit carries on to the space check by itself when this is done. Press s twice to skip, Esc to stop."))
	return b.String()
}

// installFailedView shows which step did not finish and why.
func (m Model) installFailedView(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(th.Danger.Render(ctx.Icons.Fail + " hatchzip was not installed"))
	b.WriteString("\n\n")
	b.WriteString(activity.View(ctx, m.inst.rows, m.frame, ctx.Width, max(3, ctx.BodyHeight-8)))
	b.WriteString("\n\n")
	b.WriteString(para(ctx, th.Base, m.inst.reason))
	b.WriteString("\n")
	b.WriteString(para(ctx, th.Muted, "Press r to go back and try again, or l to read what Scoop said."))
	return b.String()
}

// checkView is the space plan, or why the check did not pass.
func (m Model) checkView(ctx uictx.Context) string {
	switch m.check.Verdict {
	case extractengine.VerdictFits:
		return m.planView(ctx)
	case extractengine.VerdictNoSpace:
		return m.noSpaceView(ctx)
	default:
		return m.checkProblemView(ctx)
	}
}

// heading is the line over a card: what is about to happen to which archive.
func (m Model) heading(ctx uictx.Context) string {
	verb := "Extract "
	if m.resuming {
		verb = "Resume "
	}
	return ctx.Theme.Title.Render(ctx.Truncate(verb + m.name))
}

// planRow is one label and value of the space plan.
type planRow struct{ label, value string }

// spaceRows is the space plan as rows. Every size in it came from hatchzip's
// rounded text, so every one says "about" (rule 84).
func (m Model) spaceRows() []planRow {
	p := m.check.Plan
	free := freeLabel(m.req.OutDir)
	reserve := ""
	if p.Reserved > 0 {
		reserve = "  (+ " + about(p.Reserved) + " kept free)"
	}
	var rows []planRow
	if p.Available > 0 {
		rows = append(rows, planRow{free, about(p.Available)})
	}
	if m.req.Mode == extractengine.ModeKeepArchive {
		if p.Required > 0 {
			rows = append(rows, planRow{"Keeping the archive needs", about(p.Required) + reserve})
		}
	} else {
		if p.StandardNeeds > 0 {
			rows = append(rows, planRow{"A normal extract needs", about(p.StandardNeeds)})
		}
		if p.Required > 0 {
			rows = append(rows, planRow{"In place needs", about(p.Required) + reserve})
		}
	}
	if p.AvailableAfter > 0 {
		rows = append(rows, planRow{"Free afterwards", about(p.AvailableAfter)})
	}
	return rows
}

// renderRows draws label and value columns, the labels padded to one width.
func renderRows(th *theme.Theme, rows []planRow) []string {
	w := 0
	for _, r := range rows {
		w = max(w, ansi.StringWidth(r.label))
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		pad := strings.Repeat(" ", w-ansi.StringWidth(r.label)+3)
		out = append(out, th.Muted.Render(r.label+pad)+th.Base.Render(r.value))
	}
	return out
}

// archiveLine is the card's first line: the archive, its format, its size
// and how many items it holds.
func (m Model) archiveLine(ctx uictx.Context, inner int) string {
	th := ctx.Theme
	p := m.check.Plan
	var facts []string
	if f := firstNonEmpty(p.Format, string(m.req.Set.Format)); f != "" {
		facts = append(facts, f)
	}
	if n := len(m.req.Set.Parts); n > 1 {
		facts = append(facts, strconv.Itoa(n)+" parts")
	}
	if size := firstPositive(p.ArchiveBytes, m.req.Set.Bytes); size > 0 {
		facts = append(facts, about(size))
	}
	if p.Entries > 0 {
		items := commas(int64(p.Entries)) + " item" + plural(p.Entries)
		if p.Resuming {
			// While resuming, hatchzip counts the entries still to write.
			items += " left"
		}
		facts = append(facts, items)
	}
	rest := ""
	if len(facts) > 0 {
		rest = sep + strings.Join(facts, sep)
	}
	name := m.name
	if room := inner - ansi.StringWidth(rest); ansi.StringWidth(name) > room {
		name = ansi.Truncate(name, max(room, 8), ellipsis(ctx))
	}
	return th.Subtitle.Render(name) + th.Muted.Render(rest)
}

// planView is the card of a check that passed.
func (m Model) planView(ctx uictx.Context) string {
	th := ctx.Theme
	p := m.check.Plan
	inner := cardInner(ctx)

	lines := []string{m.archiveLine(ctx, inner)}
	lead := "Unpacks to " + about(p.TotalBytes) + " in"
	if p.Resuming {
		lead = "Resuming: " + about(p.LeftBytes) + " of " + sizeOnly(p.TotalBytes) + " left, into"
	}
	for _, l := range pathLines(lead, m.req.OutDir, inner, ascii(ctx)) {
		lines = append(lines, th.Base.Render(l))
	}
	if rows := renderRows(th, m.spaceRows()); len(rows) > 0 {
		lines = append(lines, "")
		lines = append(lines, rows...)
	}
	lines = append(lines, "")
	for _, l := range hang(ctx.Icons.Tick, m.fitsVerdict(), inner) {
		lines = append(lines, th.Success.Render(l))
	}
	lines = append(lines, m.otherDriveLines(ctx, inner)...)

	var b strings.Builder
	b.WriteString(m.heading(ctx))
	b.WriteString("\n")
	b.WriteString(th.CardFor(ascii(ctx)).Render(strings.Join(lines, "\n")))
	// hatchzip's own notes sit under the card, as many as there is room for.
	room := min(3, ctx.BodyHeight-len(lines)-3)
	for i, n := range p.Notes {
		if i >= room {
			break
		}
		b.WriteString("\n")
		b.WriteString(th.Muted.Render(ctx.Truncate(" hatchzip: " + n)))
	}
	return b.String()
}

// otherDriveLines is the one thing a folder on another drive changes about
// extracting in place: the archive still goes, but the room it gives back is
// not where the files land. It shows whichever way the archive is treated,
// since it is also what pressing i would get; nothing on the archive's own
// drive.
func (m Model) otherDriveLines(ctx uictx.Context, width int) []string {
	if !m.otherDrive() {
		return nil
	}
	text := "That folder is on another drive. In place still removes the archive, but the extracted files need their full size free on " + driveName(m.req.OutDir) + "."
	return styled(ctx.Theme.Warning, hang(ctx.Icons.Warn, text, width))
}

// fitsVerdict is the sentence beside the tick. Whether there is a tick at
// all comes only from hatchzip's exit code.
func (m Model) fitsVerdict() string {
	p := m.check.Plan
	keep := m.req.Mode == extractengine.ModeKeepArchive
	switch {
	case p.AllDone:
		return "Every file is already extracted. Enter lets hatchzip finish the clean-up."
	case keep && m.autoKeep && !m.modeChosen:
		return "There is room to keep the archive, so Devpit keeps it: nothing is removed. Press i to extract in place instead."
	case keep:
		return "Fits. The archive is kept and nothing is removed."
	case m.resuming:
		return "Fits. hatchzip carries on where it stopped, removing the archive piece by piece as its files are written."
	default:
		return "Fits in place. The archive is removed piece by piece as its files are written."
	}
}

// noSpaceView is the card of a check that found too little space. Nothing on
// disk was changed.
func (m Model) noSpaceView(ctx uictx.Context) string {
	th := ctx.Theme
	p := m.check.Plan
	inner := cardInner(ctx)
	keep := m.req.Mode == extractengine.ModeKeepArchive

	what := "Not enough space, even in place."
	if keep {
		what = "Not enough space to keep the archive."
	}
	if p.ShortBy > 0 {
		what += " hatchzip says " + about(p.ShortBy) + " more is needed."
	}
	what += " Nothing was changed."

	var lines []string
	for _, l := range hang(ctx.Icons.Fail, what, inner) {
		lines = append(lines, th.Danger.Render(l))
	}
	if rows := renderRows(th, m.spaceRows()); len(rows) > 0 {
		lines = append(lines, "")
		lines = append(lines, rows...)
	}
	if other := m.otherDriveLines(ctx, inner); len(other) > 0 {
		lines = append(lines, "")
		lines = append(lines, other...)
	}

	hint := "Free some space first: Full Scan in the Clean menu can find it."
	switch {
	case m.offersInPlace():
		hint = "Press i to extract in place instead: it needs far less space."
	case m.offersFolder():
		hint = "Free some space first: Full Scan in the Clean menu can find it. Or press o to extract into a folder on a drive with more room."
	}
	return m.heading(ctx) + "\n" +
		th.CardFor(ascii(ctx)).Render(strings.Join(lines, "\n")) + "\n" +
		para(ctx, th.Muted, hint)
}

// checkProblemView is a check that ended any other way: damage, a solid RAR,
// a Devpit bug, or something hatchzip could not do.
func (m Model) checkProblemView(ctx uictx.Context) string {
	th := ctx.Theme
	c := m.check
	var what, hint string
	switch c.Verdict {
	case extractengine.VerdictDamaged:
		what = "hatchzip says the archive is damaged. Nothing was changed."
	case extractengine.VerdictBadArgs:
		what = "Devpit asked hatchzip something it did not understand. This is a Devpit bug; l shows the details."
	case extractengine.VerdictSolid:
		what = "This RAR is solid, so it cannot be extracted in place. Nothing was removed."
		hint = "Press k to extract it and keep the archive."
		if c.Plan.TotalBytes > 0 {
			hint = "Press k to extract it and keep the archive. That needs " + about(c.Plan.TotalBytes) + " free."
		}
	default:
		// Exit 1 is hatchzip's plain "no", with the reason on its error
		// line: an archive it cannot read, a folder that belongs to another
		// extraction. Any other code is one Devpit does not know.
		what = "hatchzip could not check this archive. Nothing was changed."
		if c.Exit > 1 {
			what = "hatchzip ended with code " + strconv.Itoa(c.Exit) + ". Nothing was changed."
		}
	}
	var b strings.Builder
	b.WriteString(m.heading(ctx))
	b.WriteString("\n\n")
	b.WriteString(strings.Join(styled(th.Danger, hang(ctx.Icons.Fail, what, ctx.Width)), "\n"))
	if c.Error != "" && c.Verdict != extractengine.VerdictSolid {
		b.WriteString("\n")
		b.WriteString(para(ctx, th.Muted, "hatchzip: "+c.Error))
	}
	if hint != "" {
		b.WriteString("\n\n")
		b.WriteString(para(ctx, th.Muted, hint))
	}
	return b.String()
}

// passwordView asks for the archive's password. The field is masked: no
// frame ever holds what was typed.
func (m Model) passwordView(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(para(ctx, th.Base, m.name+" is protected with a password."))
	b.WriteString("\n\n")
	b.WriteString(th.Muted.Render("Password  ") + styleInput(m.pass, th).View())
	if m.wrongPass {
		b.WriteString("\n\n")
		b.WriteString(th.Danger.Render(ctx.Icons.Fail + " That password did not open the archive."))
	}
	b.WriteString("\n\n")
	b.WriteString(para(ctx, th.Muted, "Devpit hands it to hatchzip for this run only, in memory. It is never saved, so a later Resume asks for it again."))
	return b.String()
}

// styleInput applies the theme to a text field, the way the other screens
// style theirs: accent prompt, muted placeholder, plain text, a still caret.
func styleInput(ti textinput.Model, th *theme.Theme) textinput.Model {
	state := textinput.StyleState{
		Text: th.Base, Placeholder: th.Muted, Suggestion: th.Muted, Prompt: th.Accent,
	}
	st := ti.Styles()
	st.Focused, st.Blurred = state, state
	st.Cursor.Color = th.Palette.Accent
	st.Cursor.Blink = false
	ti.SetStyles(st)
	return ti
}

// runningView is the live progress screen, in the shape of Share's copy: the
// bar, how much and how fast, how many items, then what is happening now.
func (m Model) runningView(ctx uictx.Context) string {
	th := ctx.Theme
	p := m.prog
	inPlace := m.req.Mode == extractengine.ModeInPlace

	frac := 0.0
	if p.TotalBytes > 0 {
		frac = min(0.99, float64(p.DoneBytes)/float64(p.TotalBytes))
	}
	mode := "in place"
	if !inPlace {
		mode = "keeping the archive"
	}

	var b strings.Builder
	b.WriteString(th.Title.Render("Extracting") + th.Muted.Render(ctx.Truncate(" "+m.name+sep+mode)) + "\n")
	b.WriteString(" " + ctx.Track(frac, min(48, max(10, ctx.Width-12))) + th.Info.Render(fmt.Sprintf("  %3d%%", m.percent())) + "\n")

	amount := " " + soFar(p.DoneBytes) + " so far"
	if p.TotalBytes > 0 {
		amount = " " + soFar(p.DoneBytes) + " of " + sizeOnly(p.TotalBytes)
	}
	b.WriteString(th.Base.Bold(true).Render(amount))
	if p.Speed >= 1 {
		b.WriteString(th.Muted.Render(sep + rate(p.Speed)))
	}
	if p.ETA > 0 && p.Phase == extractengine.PhaseWriting && !m.pausing {
		b.WriteString(th.Muted.Render(sep + clock(p.ETA) + " left"))
	}
	b.WriteString("\n")

	counts := " " + commas(int64(p.DoneItems)) + " items"
	if p.TotalItems > 0 {
		counts = " " + commas(int64(p.DoneItems)) + " of " + commas(int64(p.TotalItems)) + " items"
	}
	b.WriteString(th.Muted.Render(counts + sep + clock(p.Elapsed)))
	b.WriteString("\n\n")

	// What is happening now, then two facts that move with it.
	free := freeLabel(m.req.OutDir)
	labelW := max(ansi.StringWidth(free), len("Finishing"))
	label, detail := m.phaseRow(ctx)
	b.WriteString(activity.RowView(ctx, activity.Row{Label: label, State: activity.Running, Percent: -1, Detail: detail}, m.frame, ctx.Width, labelW))
	fact := func(name, value string) {
		b.WriteString("\n   " + th.Base.Render(name+strings.Repeat(" ", labelW-ansi.StringWidth(name))) + "  " + th.Muted.Render(value))
	}
	if inPlace && p.ArchiveBytes > 0 {
		value := "now " + filepicker.FormatSize(p.ArchiveBytes)
		if was := m.req.Set.Bytes; !m.resuming && was > p.ArchiveBytes {
			value += ", was " + filepicker.FormatSize(was)
		}
		fact("Archive", value)
	}
	if p.FreeBytes > 0 {
		fact(free, about(int64(min(p.FreeBytes, 1<<62)))) //nolint:gosec // capped below the int64 limit
	}

	b.WriteString("\n\n")
	note := " Esc pauses safely. Resume carries on from here."
	if m.pausing || p.Phase == extractengine.PhasePausing || p.Phase == extractengine.PhaseStopping {
		note = " Nothing is lost: the files written so far stay, and Resume carries on."
	}
	b.WriteString(th.Muted.Render(note))
	if m.autoResumes > 0 {
		b.WriteString("\n\n")
		for i, l := range wrapWords("hatchzip reported the disk full too early (a known hatchzip issue). Devpit resumed it "+times(m.autoResumes)+".", ctx.Width-2) {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(th.Muted.Render(" " + l))
		}
	}
	return b.String()
}

// phaseRow names what hatchzip is doing right now.
func (m Model) phaseRow(ctx uictx.Context) (label, detail string) {
	p := m.prog
	switch {
	case p.Phase == extractengine.PhaseStopping:
		return "Stopping", "hatchzip did not pause in time, stopping it (progress is kept)"
	case p.Phase == extractengine.PhasePausing || m.pausing:
		return "Pausing", "hatchzip is finishing the part it is on…"
	case p.Phase == extractengine.PhaseAutoResume:
		return "Resuming", "starting hatchzip again"
	case p.Phase == extractengine.PhaseFinishing:
		return "Finishing", "hatchzip is tidying up"
	case p.Phase == extractengine.PhaseStarting:
		return "Starting", "hatchzip is opening the archive"
	}
	current := p.Current
	if current == "" {
		current = "working" + ellipsis(ctx)
	}
	return "Writing", current
}

// doneView is the end of a good run.
func (m Model) doneView(ctx uictx.Context) string {
	th := ctx.Theme
	o := m.out
	inner := cardInner(ctx)

	head := th.Success.Bold(true).Render(ctx.Icons.Tick + " Done. " + about(o.Last.TotalBytes) + " extracted")
	var facts []string
	// A resumed run only counted the items that were left, which is not
	// what "extracted" means on this line, so it is left out there.
	if n := o.Last.TotalItems; n > 0 && !m.resuming {
		facts = append(facts, commas(int64(n))+" item"+plural(n))
	}
	if o.Elapsed > 0 {
		facts = append(facts, clock(o.Elapsed))
	}
	if len(facts) > 0 {
		head += th.Muted.Render(sep + strings.Join(facts, sep))
	}

	lines := []string{head, ""}
	for _, l := range pathLines("The files are in", m.req.OutDir, inner, ascii(ctx)) {
		lines = append(lines, th.Base.Render(l))
	}
	after := m.name + " was kept."
	if m.req.Mode == extractengine.ModeInPlace {
		after = m.name + " was removed."
	}
	if m.freeKnown {
		after += " " + freeLabel(m.req.OutDir) + " " + about(int64(min(m.freeAfter, 1<<62))) //nolint:gosec // capped below the int64 limit
	}
	lines = append(lines, styled(th.Muted, wrapWords(after, inner))...)
	if m.autoResumes > 0 {
		lines = append(lines, styled(th.Muted, wrapWords("hatchzip reported the disk full too early "+times(m.autoResumes)+", and Devpit resumed it by itself"+eachTime(m.autoResumes)+".", inner))...)
	}
	lines = append(lines, styled(th.Muted, logLines(m.logPath(), inner))...)
	return th.CardFor(ascii(ctx)).Render(strings.Join(lines, "\n")) + "\n\n" + th.Muted.Render("Press Enter to go back.")
}

// pausedView is a run that paused, or that Devpit stopped after hatchzip did
// not pause in time. Either way it can be resumed.
func (m Model) pausedView(ctx uictx.Context) string {
	th := ctx.Theme
	o := m.out

	head := "Paused. " + soFar(o.Last.DoneBytes) + " extracted so far."
	if o.Last.TotalBytes > 0 {
		head = "Paused. " + soFar(o.Last.DoneBytes) + " of " + sizeOnly(o.Last.TotalBytes) + " extracted so far."
	}
	if o.Kind == extractengine.KindStopped {
		head = "Stopped. hatchzip did not pause in time, so Devpit stopped it. Progress is saved; Resume carries on."
	}

	var b strings.Builder
	b.WriteString(strings.Join(styled(th.Warning, hang(pauseMark(ctx), head, ctx.Width)), "\n"))
	b.WriteString("\n")
	b.WriteString(strings.Join(styled(th.Base, pathLines("The files written so far are in", m.req.OutDir, ctx.Width, ascii(ctx))), "\n"))
	b.WriteString("\n")
	switch {
	case m.req.Mode != extractengine.ModeInPlace:
		b.WriteString(para(ctx, th.Muted, m.name+" is unchanged."))
	case o.Last.ArchiveBytes > 0:
		b.WriteString(para(ctx, th.Muted, m.name+" now holds only what is left ("+filepicker.FormatSize(o.Last.ArchiveBytes)+")."))
	default:
		b.WriteString(para(ctx, th.Muted, m.name+" now holds only what is left."))
	}
	if log := logLines(m.logPath(), ctx.Width); len(log) > 0 {
		b.WriteString("\n")
		b.WriteString(strings.Join(styled(th.Muted, log), "\n"))
	}
	b.WriteString("\n\n")
	b.WriteString(para(ctx, th.Muted, "Press Enter to resume now. To resume later, open Extract big archive in place from the Clean menu."))
	return b.String()
}

// failedView is a run that ended any other way. The exit code picked the
// card; hatchzip's own words only fill in the detail line.
func (m Model) failedView(ctx uictx.Context) string {
	th := ctx.Theme
	o := m.out
	var what, hint string
	showError := false
	switch o.Kind {
	case extractengine.KindSolid:
		what = "This RAR is solid, so it cannot be extracted in place. Nothing was removed."
		hint = "Press k to extract it and keep the archive."
		if o.Last.TotalBytes > 0 {
			hint = "Press k to extract it and keep the archive. That needs " + about(o.Last.TotalBytes) + " free."
		}
	case extractengine.KindNoSpace:
		what = "The disk filled up. Progress is saved. Free some space, then r resumes."
	case extractengine.KindDamaged:
		what = "hatchzip found damage in the archive and stopped."
		if o.Last.DoneBytes == 0 {
			what = "hatchzip found damage in the archive. Nothing was removed."
		}
		showError = true
	case extractengine.KindBadArgs:
		what = "Devpit asked hatchzip something it did not understand. This is a Devpit bug; l shows the details."
	default:
		switch {
		case o.Exit < 0 && o.Error != "":
			// hatchzip never ran: the record could not be written, or the
			// program could not be started.
			what = "The extraction could not start: " + o.Error
		case o.Error != "":
			what = "hatchzip stopped: " + o.Error
		case o.Exit > 0:
			what = "hatchzip ended with code " + strconv.Itoa(o.Exit) + "."
		default:
			what = "hatchzip stopped without saying why."
		}
	}

	var b strings.Builder
	b.WriteString(strings.Join(styled(th.Danger, hang(ctx.Icons.Fail, what, ctx.Width)), "\n"))
	if showError && o.Error != "" {
		b.WriteString("\n")
		b.WriteString(para(ctx, th.Muted, "hatchzip: "+o.Error))
	}
	if o.CanResume && o.Kind != extractengine.KindSolid {
		saved := "Files written so far are in"
		if o.Kind != extractengine.KindNoSpace {
			saved = "Progress is saved. Files written so far are in"
		}
		b.WriteString("\n")
		b.WriteString(strings.Join(styled(th.Base, pathLines(saved, m.req.OutDir, ctx.Width, ascii(ctx))), "\n"))
	}
	if m.autoResumes > 0 {
		b.WriteString("\n")
		b.WriteString(para(ctx, th.Muted, "hatchzip reported the disk full too early "+times(m.autoResumes)+", and Devpit already resumed it by itself"+eachTime(m.autoResumes)+"."))
	}
	if log := logLines(m.logPath(), ctx.Width); len(log) > 0 {
		b.WriteString("\n")
		b.WriteString(strings.Join(styled(th.Muted, log), "\n"))
	}
	if hint != "" {
		b.WriteString("\n\n")
		b.WriteString(para(ctx, th.Muted, hint))
	}
	return b.String()
}

// restoringView is the wait while hatchzip rebuilds the archive.
func (m Model) restoringView(ctx uictx.Context) string {
	th := ctx.Theme
	row := activity.Row{Label: "Restoring", State: activity.Running, Percent: -1, Detail: m.restoreLine, Elapsed: m.restoreTook}
	if row.Detail == "" {
		row.Detail = "hatchzip is rebuilding the archive"
	}
	var b strings.Builder
	b.WriteString(th.Title.Render("Restoring") + th.Muted.Render(ctx.Truncate(" "+m.name)))
	b.WriteString("\n\n")
	b.WriteString(activity.RowView(ctx, row, m.frame, ctx.Width, len(row.Label)))
	b.WriteString("\n\n")
	b.WriteString(para(ctx, th.Muted, "hatchzip puts the files it has not extracted yet back into a working archive. The files already extracted stay where they are."))
	return b.String()
}

// restoredView is how the restore ended.
func (m Model) restoredView(ctx uictx.Context) string {
	th := ctx.Theme
	if m.restoreErr != "" {
		return strings.Join(styled(th.Danger, hang(ctx.Icons.Fail, m.restoreErr, ctx.Width)), "\n") + "\n\n" +
			para(ctx, th.Muted, "Press r to go back to Resume or Restore, or l to read what hatchzip said.")
	}
	inner := cardInner(ctx)
	head := th.Success.Bold(true).Render(ctx.Icons.Tick + " Restored. " + ansi.Truncate(m.name, max(inner-40, 12), ellipsis(ctx)) + " is a working archive again")
	lines := []string{head, ""}
	for _, l := range pathLines("Files already extracted stay in", m.req.OutDir, inner, ascii(ctx)) {
		lines = append(lines, th.Base.Render(l))
	}
	return th.CardFor(ascii(ctx)).Render(strings.Join(lines, "\n")) + "\n\n" + th.Muted.Render("Press Enter to go back.")
}

// detailsView shows hatchzip's (or Scoop's) own last lines, for the day the
// plain words are not enough.
func (m Model) detailsView(ctx uictx.Context) string {
	th := ctx.Theme
	title, lines, exit := "What hatchzip said", m.out.Lines, m.out.Exit
	switch m.state {
	case stateCheck:
		lines, exit = m.check.Lines, m.check.Exit
		// Anything the plan reader did not recognise is in these lines too:
		// it changed nothing, and here it can be read.
	case stateInstallFailed:
		title, lines, exit = "What Scoop said", m.inst.log, 0
	}
	// Where the whole of it is kept, indented like the lines above it.
	var log []string
	for _, l := range logLines(m.logPath(), ctx.Width-2) {
		log = append(log, th.Muted.Render("  "+l))
	}
	var b strings.Builder
	b.WriteString(th.Subtitle.Render(title))
	b.WriteString("\n\n")
	if len(lines) == 0 {
		b.WriteString(th.Muted.Render("  Nothing was printed."))
	} else {
		b.WriteString(activity.LogView(ctx, lines, ctx.Width, max(3, ctx.BodyHeight-5-len(log))))
	}
	if exit != 0 || len(log) > 0 {
		b.WriteString("\n")
	}
	if exit != 0 {
		b.WriteString("\n")
		b.WriteString(th.Muted.Render("  Exit code " + strconv.Itoa(exit)))
	}
	if len(log) > 0 {
		b.WriteString("\n")
		b.WriteString(strings.Join(log, "\n"))
	}
	return b.String()
}

// questionWidth is how wide a confirmation's detail runs before it folds.
const questionWidth = 72

// detail folds the paragraphs of a confirmation's detail at their spaces, so
// the card keeps a tidy width and a file name is never split at a hyphen.
func detail(paragraphs ...string) string {
	out := make([]string, 0, len(paragraphs))
	for _, p := range paragraphs {
		out = append(out, strings.Join(wrapWords(p, questionWidth), "\n"))
	}
	return strings.Join(out, "\n\n")
}

// runQuestion is the run confirmation: what happens to which archive, and
// that in place removes it as it goes. The default answer is No.
func (m Model) runQuestion() (question, more string) {
	out := m.req.OutDir
	esc := "Esc pauses safely at any time."
	switch {
	case m.req.Mode == extractengine.ModeKeepArchive && m.resuming:
		return "Resume extracting " + m.name + " and keep the archive?",
			detail("hatchzip carries on where it stopped, into "+out+". The archive is not changed and nothing is removed.", esc)
	case m.req.Mode == extractengine.ModeKeepArchive:
		return "Extract " + m.name + " and keep the archive?",
			detail("Devpit will unpack "+m.name+" into "+out+". The archive is not changed and nothing is removed.", esc)
	case m.resuming:
		return "Resume extracting " + m.name + " in place and remove the archive?",
			detail("hatchzip carries on where it stopped, into "+out+", and goes on cutting the archive down as each part is written. When it finishes, "+m.name+" is gone.",
				"Keep another copy if this archive is the only one. "+esc)
	default:
		return "Extract " + m.name + " in place and remove the archive?",
			detail("Devpit will unpack "+m.name+" into "+out+" and hatchzip will cut the archive down as each part is written. If it stops, the files written so far stay and Resume carries on. When it finishes, "+m.name+" is gone.",
				"Keep another copy if this archive is the only one. "+esc)
	}
}

// installQuestion is the install confirmation. It shows the exact commands,
// which come from the catalog and Devpit's list of known buckets.
func (m Model) installQuestion() (question, more string) {
	question = "Install hatchzip?"
	if m.tool.kind == toolOld {
		question = "Update hatchzip?"
	}
	lines := []string{"Devpit will run exactly:"}
	for _, s := range m.tool.steps {
		lines = append(lines, "  "+strings.Join(s.Argv, " "))
	}
	lines = append(lines, "", "No admin rights are needed.")
	return question, strings.Join(lines, "\n")
}

// restoreQuestion is the restore confirmation.
func (m Model) restoreQuestion() (question, more string) {
	return "Restore " + m.name + "?",
		detail("hatchzip rebuilds the archive from the entries it has not extracted yet. Files already extracted stay in " + m.req.OutDir + ".")
}

// Small pieces of wording and layout.

// ascii reports the plain glyph tier.
func ascii(ctx uictx.Context) bool { return ctx.Icons.Tier == icons.TierASCII }

// ellipsis is the mark a cut line ends with, in the tier's spelling.
func ellipsis(ctx uictx.Context) string {
	if ascii(ctx) {
		return "..."
	}
	return "…"
}

// pauseMark is the glyph of a paused run.
func pauseMark(ctx uictx.Context) string {
	if ascii(ctx) {
		return "||"
	}
	return "‖"
}

// cardInner is how wide a card's text may run on this terminal.
func cardInner(ctx uictx.Context) int {
	return max(20, min(ctx.Width-4, cardInnerMax))
}

// about writes a size that came from hatchzip's rounded text, or was moving
// when it was read. Zero means the text did not say.
func about(n int64) string {
	if n <= 0 {
		return "an unknown amount"
	}
	return "about " + filepicker.FormatSize(n)
}

// soFar is a running count of bytes written. It is counted from file sizes
// while hatchzip writes, so it says "about" as well, and unlike a size from
// the plan it can honestly be nothing yet.
func soFar(n int64) string { return "about " + filepicker.FormatSize(n) }

// rate writes a speed, "412 MB/s": whole numbers from 100 up, where a
// decimal would only flicker.
func rate(bytesPerSecond float64) string {
	size := filepicker.FormatSize(int64(bytesPerSecond))
	if num, unit, ok := strings.Cut(size, " "); ok && len(num) >= 5 {
		if whole, _, cut := strings.Cut(num, "."); cut {
			size = whole + " " + unit
		}
	}
	return size + "/s"
}

// sizeOnly is the second size of an "about X of Y" pair, which the one
// "about" in front covers.
func sizeOnly(n int64) string {
	if n <= 0 {
		return "an unknown amount"
	}
	return filepicker.FormatSize(n)
}

// freeLabel is "Free on D:" for the drive holding path.
func freeLabel(path string) string {
	if len(path) >= 2 && path[1] == ':' {
		return "Free on " + strings.ToUpper(path[:1]) + ":"
	}
	return "Free space"
}

// clock writes a duration the way people say it: whole seconds under a
// minute, then minutes and seconds, then hours and minutes.
func clock(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// commas writes a whole number with thousands separators, as "12,408".
func commas(n int64) string {
	digits := strconv.FormatInt(max(n, 0), 10)
	var out []byte
	for i := range len(digits) {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, digits[i])
	}
	return string(out)
}

// plural is the "s" on a count.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// times is "once" or "N times".
func times(n int) string {
	if n == 1 {
		return "once"
	}
	return strconv.Itoa(n) + " times"
}

// eachTime ends a sentence about something done n times.
func eachTime(n int) string {
	if n == 1 {
		return ""
	}
	return " each time"
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func firstPositive(a, b int64) int64 {
	if a > 0 {
		return a
	}
	return b
}

// wrapWords folds text to width at its spaces only. Prose wrapping breaks
// after a hyphen too, which would split "Game-Backup.zip" across two lines;
// a file name stays whole here. A word longer than a line is cut where it
// must be.
func wrapWords(text string, width int) []string {
	if width < 8 {
		return []string{text}
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case ansi.StringWidth(line)+1+ansi.StringWidth(word) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
		for ansi.StringWidth(line) > width {
			head := breakPath(ansi.Truncate(line, width, ""))
			lines = append(lines, head)
			line = strings.TrimPrefix(line, head)
		}
	}
	return append(lines, line)
}

// breakPath shortens the first piece of a word too long for one line so it
// ends at a path separator, when there is one in its second half: a long
// folder path then folds between folders, not in the middle of a name.
func breakPath(head string) string {
	if i := strings.LastIndexAny(head, `\/`); i >= len(head)/2 {
		return head[:i+1]
	}
	return head
}

// hang is "mark text" folded to width, the lines after the first indented
// under the text.
func hang(mark, text string, width int) []string {
	indent := strings.Repeat(" ", ansi.StringWidth(mark)+1)
	lines := wrapWords(text, width-len(indent))
	for i, l := range lines {
		if i == 0 {
			lines[i] = mark + " " + l
		} else {
			lines[i] = indent + l
		}
	}
	return lines
}

// pathLines is "lead path" on one line when it fits in width, and the path on
// a line of its own under the lead when it does not, cut from the left so the
// folder's own name stays visible.
func pathLines(lead, path string, width int, plain bool) []string {
	if path == "" {
		return wrapWords(lead+" the folder beside the archive", width)
	}
	if ansi.StringWidth(lead)+1+ansi.StringWidth(path) <= width {
		return []string{lead + " " + path}
	}
	mark := "…"
	if plain {
		mark = "..."
	}
	room := max(width-2, 8)
	if ansi.StringWidth(path) > room {
		tail := ansi.TruncateLeft(path, ansi.StringWidth(path)-room+ansi.StringWidth(mark), "")
		// Start the kept part at a folder, not in the middle of a name.
		if i := strings.IndexAny(tail, `\/`); i > 0 && i <= len(tail)/3 {
			tail = tail[i:]
		}
		path = mark + tail
	}
	return append(wrapWords(lead, width), "  "+path)
}

// styled renders every line in one style.
func styled(s lipgloss.Style, lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = s.Render(l)
	}
	return out
}

// para folds a paragraph to the terminal width at its spaces and renders it.
func para(ctx uictx.Context, s lipgloss.Style, text string) string {
	return strings.Join(styled(s, wrapWords(text, ctx.Width)), "\n")
}
