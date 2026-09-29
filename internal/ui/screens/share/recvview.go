package share

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/share/errmap"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/activity"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// roundDuration writes a time left the way people say it: whole seconds under
// a minute, then minutes and seconds, then hours and minutes.
func roundDuration(d time.Duration) string {
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

// View implements uictx.Screen.
func (m recvScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	switch m.stage {
	case recvResumeAsk:
		return m.resumeView(ctx)
	case recvIP:
		return ctx.Wrap(th.Muted.Render("Type the IP address of the other PC. It is on the card the sharing PC shows.")) +
			"\n\n" + styleInput(m.ip, th).View()
	case recvListing:
		return th.Muted.Render(ctx.SpinnerFrame(m.frame) + " Talking to the other PC…")
	case recvCreds:
		return m.credsView(ctx)
	case recvShares:
		return th.Muted.Render("Which shared folder do you want to copy from "+m.host+"?") + "\n\n" + m.shMenu.View(ctx)
	case recvDest:
		return m.picker.View(ctx)
	case recvChecking:
		return th.Muted.Render(ctx.SpinnerFrame(m.frame)+" Measuring the folder. Nothing is copied yet…") + "\n" +
			th.Muted.Render("A big folder can take a minute.")
	case recvCheck:
		return m.checkView(ctx)
	case recvCopy:
		return m.copyView(ctx)
	case recvDone:
		return m.doneView(ctx)
	case recvFailed:
		return m.failedView(ctx)
	default:
		return m.errorScreen(ctx)
	}
}

// resumeView asks whether to carry on an interrupted copy.
func (m recvScreen) resumeView(ctx uictx.Context) string {
	th := ctx.Theme
	j := m.job
	var b strings.Builder
	b.WriteString(th.Title.Render("A copy was interrupted") + "\n\n")
	b.WriteString(th.Base.Render(fmt.Sprintf("From  %s  (%s)", `\\`+j.Host+`\`+j.Share, j.User)) + "\n")
	b.WriteString(th.Base.Render("To    "+j.Dest) + "\n")
	b.WriteString(th.Base.Render(fmt.Sprintf("Done  %s of %s", recv.FormatBytes(j.DoneBytes), recv.FormatBytes(j.TotalBytes))) + "\n\n")
	if j.LastError != "" {
		b.WriteString(ctx.Wrap(th.Muted.Render(j.LastError)) + "\n\n")
	}
	b.WriteString(ctx.Wrap(th.Muted.Render("Finished files are skipped. A half-copied file carries on where it stopped.")))
	return b.String()
}

// credsView draws the sign-in form.
func (m recvScreen) credsView(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	switch m.credsFor {
	case credsForList:
		b.WriteString(th.Base.Render("That PC wants a sign-in before it lists its folders.") + "\n")
	case credsForResume:
		b.WriteString(th.Base.Render("Windows has forgotten the sign-in. Type the password again.") + "\n")
	default:
		b.WriteString(th.Base.Render("Sign in to "+`\\`+m.host+`\`+m.share+".") + "\n")
	}
	b.WriteString(ctx.Wrap(th.Muted.Render("Use the user name and password from the sharing PC's card.")) + "\n")
	b.WriteString(ctx.Wrap(th.Muted.Render("A Microsoft account signs in as MicrosoftAccount\\you@example.com, with its password, not a PIN.")) + "\n\n")
	b.WriteString(th.Muted.Render("User      ") + styleInput(m.user, th).View() + "\n")
	b.WriteString(th.Muted.Render("Password  ") + styleInput(m.pass, th).View() + "\n\n")
	b.WriteString(ctx.Wrap(th.Muted.Render("The password is used once to sign in. Devpit does not save it.")))
	return b.String()
}

// checkView shows what the dry run found and whether the copy can start.
func (m recvScreen) checkView(ctx uictx.Context) string {
	th := ctx.Theme
	c := m.check
	label := func(s string) string { return th.Muted.Render(fmt.Sprintf("%-12s", s)) }
	var b strings.Builder
	b.WriteString(th.Title.Render("Ready to copy") + "\n\n")
	b.WriteString(label("From") + th.Base.Render(`\\`+m.host+`\`+m.share) + "\n")
	b.WriteString(label("To") + th.Base.Render(fit(m.dest, max(ctx.Width-16, 20))) + "\n")
	b.WriteString(label("Size") + th.Base.Bold(true).Render(recv.FormatBytes(c.Plan.Bytes)) +
		th.Muted.Render(fmt.Sprintf("  in %s files", commas(c.Plan.Files))) + "\n")
	if c.FreeBytes > 0 {
		b.WriteString(label("Free space") + th.Base.Render(recv.FormatBytes(int64(c.FreeBytes))) + //nolint:gosec // free space fits int64
			th.Muted.Render(fileSystemNote(c.FileSystem)) + "\n")
	}
	b.WriteString("\n")
	for _, w := range c.Warnings {
		mark, style := ctx.Icons.Warn, th.Warning
		if w.Blocks {
			mark, style = ctx.Icons.Fail, th.Danger
		}
		b.WriteString(ctx.Wrap(style.Render(mark+" "+w.Text)) + "\n")
	}
	if c.OK() {
		b.WriteString(th.Success.Render(ctx.Icons.Tick + " There is room. Press Enter to start."))
	} else {
		b.WriteString("\n" + th.Muted.Render("Press Esc to go back and change the destination."))
	}
	return b.String()
}

// fileSystemNote writes "  (NTFS)" or nothing.
func fileSystemNote(fs string) string {
	if fs == "" {
		return ""
	}
	return "  (" + fs + ")"
}

// copyView is the live progress screen: one overall row, the network line,
// then the files finished most recently.
func (m recvScreen) copyView(ctx uictx.Context) string {
	th := ctx.Theme
	e := m.ev
	frac := 0.0
	if e.TotalBytes > 0 {
		frac = min(1, float64(e.DoneBytes)/float64(e.TotalBytes))
	}
	var b strings.Builder
	b.WriteString(th.Title.Render("Copying") + th.Muted.Render(" from "+m.host) + "\n")
	b.WriteString(" " + ctx.Track(frac, min(48, max(10, ctx.Width-12))) + th.Info.Render(fmt.Sprintf("  %3.0f%%", frac*100)) + "\n")
	b.WriteString(th.Base.Bold(true).Render(fmt.Sprintf(" %s of %s", recv.FormatBytes(e.DoneBytes), recv.FormatBytes(e.TotalBytes))))
	stats := []string{speedText(e.Speed), etaText(e.ETA)}
	for _, s := range stats {
		if s != "" {
			b.WriteString(th.Muted.Render("  ·  " + s))
		}
	}
	b.WriteString("\n")
	b.WriteString(th.Muted.Render(fmt.Sprintf(" %s of %s files  ·  %s", commas(e.DoneFiles), commas(e.TotalFiles), roundDuration(e.Elapsed))) + "\n\n")

	rows := m.copyRows(e)
	b.WriteString(activity.View(ctx, rows, m.frame, ctx.Width, max(3, ctx.BodyHeight-9)))
	return b.String()
}

// copyRows builds the activity rows: what is happening, then finished files.
func (m recvScreen) copyRows(e recv.Event) []activity.Row {
	var rows []activity.Row
	switch e.Phase {
	case recv.PhaseWaiting:
		detail := e.Note
		if e.NextTry > 0 {
			detail += fmt.Sprintf(" Trying again in %ds.", int(e.NextTry.Seconds()))
		}
		rows = append(rows, activity.Row{Label: "Network", State: activity.Warn, Percent: -1, Detail: detail})
	case recv.PhaseRetrying:
		rows = append(rows, activity.Row{Label: "Network", State: activity.Warn, Percent: -1, Detail: e.Note})
	default:
		rows = append(rows, activity.Row{
			Label: "Copying", State: activity.Running, Percent: -1,
			Detail: "up to 16 files at once",
		})
	}
	for i := len(e.Recent) - 1; i >= 0 && len(rows) < 8; i-- {
		rows = append(rows, activity.Row{Label: path.Base(strings.ReplaceAll(e.Recent[i], `\`, "/")), State: activity.Done, Percent: -1})
	}
	return rows
}

// doneView is the end of a good copy.
func (m recvScreen) doneView(ctx uictx.Context) string {
	th := ctx.Theme
	o := m.out
	body := th.Success.Bold(true).Render(ctx.Icons.Tick+" Done. "+recv.FormatBytes(m.job.TotalBytes)+" copied") +
		th.Muted.Render(fmt.Sprintf("  ·  %s files  ·  %s", commas(m.job.TotalFiles), roundDuration(o.Elapsed))) + "\n\n" +
		th.Base.Render("The files are in "+m.dest) + "\n" + th.Muted.Render(o.Message)
	card := th.CardFor(ctx.Icons.Tier == "ascii")
	return card.Render(body) + "\n\n" + th.Muted.Render("Press Enter to go back.")
}

// failedView lists the files that did not copy, with what to do.
func (m recvScreen) failedView(ctx uictx.Context) string {
	th := ctx.Theme
	o := m.out
	var b strings.Builder
	b.WriteString(th.Danger.Render(ctx.Icons.Fail+fmt.Sprintf(" %s files did not copy", commas(int64(len(o.Failed))))) + "\n")
	b.WriteString(th.Muted.Render(recv.FormatBytes(o.DoneBytes)+" of "+recv.FormatBytes(m.job.TotalBytes)+" was copied. "+o.Message) + "\n\n")
	limit := max(3, ctx.BodyHeight-12)
	for i, f := range o.Failed {
		if i == limit {
			b.WriteString(th.Muted.Render(fmt.Sprintf("  … and %d more", len(o.Failed)-limit)) + "\n")
			break
		}
		b.WriteString("  " + th.Base.Render(fit(f.Path, max(ctx.Width-40, 20))) + "  " + th.Muted.Render(f.Reason) + "\n")
	}
	if len(o.Failed) > 0 {
		if e, ok := errmap.Lookup(o.Failed[0].Code, errmap.PhaseCopy); ok {
			b.WriteString("\n")
			for i, s := range e.Steps {
				b.WriteString(ctx.Wrap(th.Base.Render(fmt.Sprintf("%d. %s", i+1, s))) + "\n")
			}
		}
	}
	b.WriteString("\n" + th.Muted.Render("Press r to try the failed files again."))
	return b.String()
}

// errorScreen draws an error with its steps, and the close-old-connections
// note when that has just been done.
func (m recvScreen) errorScreen(ctx uictx.Context) string {
	s := errorView(ctx, m.err, m.errPhase, m.user.Value())
	if m.dropped > 0 {
		s += "\n\n" + ctx.Theme.Muted.Render(fmt.Sprintf("Closed %d old connection(s).", m.dropped))
	}
	return s
}
