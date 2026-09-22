package clean

import (
	"fmt"
	"strings"
	"time"

	cleanengine "github.com/zubairbinshaukat/devpit/internal/clean"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// StaleBadge is the wording the scanning screen uses while it is showing the
// previous scan's rows. It is a constant so the test that pins the badge and
// the code that draws it cannot drift apart.
const StaleBadge = "stale · rescanning"

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string {
	if ctx.Width < uictx.MinWidth || ctx.Height < uictx.MinHeight {
		return ctx.Theme.Notice.Render(fmt.Sprintf(
			"Free Up Disk Space needs a bigger window.\nNow: %d×%d   Needs: %d×%d",
			ctx.Width, ctx.Height, uictx.MinWidth, uictx.MinHeight,
		))
	}

	switch m.state {
	case statePicker:
		return m.picker.View(ctx)
	case stateScanning:
		return m.scanningView(ctx)
	case stateResults:
		return m.resultsView(ctx)
	case stateConfirm:
		return m.dialog.View(ctx)
	case stateDeleting:
		return m.deletingView(ctx)
	case stateSummary:
		return m.summaryView(ctx)
	default:
		return m.menuView(ctx)
	}
}

// menuView draws the submenu.
func (m Model) menuView(ctx uictx.Context) string {
	var b strings.Builder
	b.WriteString(ctx.Theme.Muted.Render("Pick what to look through. Nothing is deleted without a preview."))
	b.WriteString("\n\n")
	b.WriteString(m.menu.View(ctx))
	if lifetime := ctx.Config.LifetimeFreedBytes; lifetime > 0 {
		b.WriteString("\n\n")
		b.WriteString(ctx.Theme.Muted.Render("Devpit has freed " + header.FormatBytes(lifetime) + " on this machine so far."))
	}
	return b.String()
}

// scanningView draws the progress bar, the live counters and whatever rows
// are already in the table, cached or fresh.
func (m Model) scanningView(ctx uictx.Context) string {
	th := ctx.Theme
	prog := m.prog
	if m.Stale() {
		prog.Note = StaleBadge
	}

	var b strings.Builder
	b.WriteString(th.Muted.Render("Scanning " + m.root))
	b.WriteString("\n")
	b.WriteString(prog.View(ctx))
	b.WriteString("\n")
	b.WriteString(th.Base.Render(m.scanCounters(ctx)))
	b.WriteString("\n")
	b.WriteString(m.table.View(ctx))
	return b.String()
}

// scanCounters is the one-line counter row under the scanning bar.
func (m Model) scanCounters(ctx uictx.Context) string {
	th := ctx.Theme
	s := m.scanStats
	parts := []string{
		fmt.Sprintf("%d folders checked", s.DirsChecked),
		fmt.Sprintf("%d found", s.Found),
		header.FormatBytes(s.Bytes),
	}
	if s.AccessDenied > 0 {
		parts = append(parts, fmt.Sprintf("%d could not be read", s.AccessDenied))
	}
	line := strings.Join(parts, "  ·  ")
	if m.Stale() {
		return th.Warning.Render(ctx.Icons.Warn+" "+StaleBadge+"  ·  ") + th.Muted.Render(line)
	}
	return th.Muted.Render(line)
}

// resultsView draws the table plus the live selection footer.
func (m Model) resultsView(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	b.WriteString(th.Muted.Render(m.resultsHeading()))
	b.WriteString("\n")
	b.WriteString(m.table.View(ctx))
	b.WriteString("\n")
	b.WriteString(m.selectionLine(ctx))
	return b.String()
}

// resultsHeading says where the results came from.
func (m Model) resultsHeading() string {
	kind := "Project junk"
	if m.full {
		kind = "Full scan"
	}
	return fmt.Sprintf("%s in %s  ·  %d item%s found in %s",
		kind, m.root, m.table.Len(), plural(m.table.Len()),
		roundSeconds(m.scanStats.Elapsed),
	)
}

// selectionLine is the "Selected: 14 items · 4.1 GB" footer. The same figure
// is offered to the app's footer through [Model.SelectionText].
func (m Model) selectionLine(ctx uictx.Context) string {
	th := ctx.Theme
	n, bytes := m.table.SelectedCount()
	if n == 0 {
		return th.Muted.Render("Nothing ticked. Space ticks a row, a ticks everything, Enter or d continues.")
	}
	return th.Accent.Render(fmt.Sprintf("Selected: %d item%s · %s", n, plural(n), header.FormatBytes(bytes))) +
		th.Muted.Render("   Enter or d to continue")
}

// SelectionText is the footer figure the app shows on the right, or "" when
// nothing is ticked.
func (m Model) SelectionText() string {
	if m.state != stateResults {
		return ""
	}
	n, bytes := m.table.SelectedCount()
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("Selected: %d item%s · %s", n, plural(n), header.FormatBytes(bytes))
}

// deletingView draws the delete bar and what is being removed right now.
func (m Model) deletingView(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(th.Muted.Render("Esc finishes the item in flight and stops. Nothing is left half-removed."))
	b.WriteString("\n\n")
	b.WriteString(m.prog.View(ctx))
	if m.freedSoFar > 0 {
		b.WriteString("\n\n")
		b.WriteString(th.Success.Render("Freed so far: " + header.FormatBytes(m.freedSoFar)))
	}
	return b.String()
}

// summaryView draws the result card and, when something was locked, the one
// thing the user can do about it.
func (m Model) summaryView(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(m.card.View(ctx))

	if len(m.locked) > 0 {
		b.WriteString("\n\n")
		if m.retrying {
			b.WriteString(th.Muted.Render("Retrying…"))
		} else {
			b.WriteString(th.Warning.Render(ctx.Icons.Warn + " " + lockedLine(m.locked)))
			b.WriteString("\n")
			b.WriteString(th.Base.Render("Close it and press R to retry."))
		}
	}
	return b.String()
}

// lockedLine names what is holding the items, in the words docs/safety.md
// asks for: never "3 items skipped".
func lockedLine(locked []cleanengine.Result) string {
	first := locked[0]
	if first.Reason != "" {
		return first.Reason
	}
	return "Something has " + shortPath(first.Path) + " open."
}

// roundSeconds renders an elapsed time the way the results heading shows it.
func roundSeconds(d time.Duration) string {
	s := d.Seconds()
	if s < 1 {
		return fmt.Sprintf("%.1f s", s)
	}
	return fmt.Sprintf("%.0f s", s)
}
