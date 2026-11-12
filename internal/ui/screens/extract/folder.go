package extract

import (
	"errors"
	"io/fs"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	extractengine "github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/pathpicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Choosing where to extract.
//
// The plan card always says which folder the files go to. From a fresh
// extraction it also offers to change it: the folder picker opens, the user
// picks a folder, and the engine names a new folder inside it. The picked
// folder is only ever the parent. Nothing else changes hands here: the new
// folder needs a check of its own, so picking one ends at the plan card with
// a new ticket, never at the run.

// folderMsg is what a picked folder came to: the new folder the engine would
// make inside it, or why it will not extract there.
type folderMsg struct {
	gen uint64
	out string
	err error
}

// offersFolder reports whether the card offers "change folder". An unfinished
// extraction carries on in the folder it was started with, so resuming never
// offers it.
func (m Model) offersFolder() bool {
	if m.resuming || m.check.Plan.Resuming || m.check.Plan.AllDone {
		return false
	}
	switch m.check.Verdict {
	case extractengine.VerdictFits, extractengine.VerdictNoSpace:
		return true
	}
	return false
}

// openFolder shows the folder picker, with the Windows dialog starting in the
// archive's own folder.
func (m Model) openFolder() (Model, tea.Cmd) {
	if m.state != stateCheck || !m.offersFolder() {
		return m, nil
	}
	// The picker lists the archive's own folder first, then the default
	// projects folder, and nothing else: its recent folders are the cleaner's
	// "scanned recently" list, which is not where anyone unpacks an archive.
	cfg := config.Default()
	cfg.DefaultProjectsFolder = m.projects
	beside := parentDir(m.req.Set.First)
	p := pathpicker.New(cfg).WithStart(beside).WithFirst(beside, "Beside the archive")
	if m.folderStat != nil {
		p = p.WithStat(m.folderStat)
	}
	if m.folderBrowse != nil {
		p = p.WithBrowse(m.folderBrowse)
	}
	m.folder, m.folderErr = p, ""
	m.state = stateFolder
	return m, nil
}

// pickFolder asks the engine, off the update loop, where the new folder would
// go inside the picked one.
func (m Model) pickFolder(parent string) (Model, tea.Cmd) {
	m.gen++
	m.folderErr = ""
	gen, in, set := m.gen, m.outDirIn, m.req.Set
	return m, func() tea.Msg {
		out, err := in(parent, set)
		return folderMsg{gen: gen, out: out, err: err}
	}
}

// onFolder takes the new folder on and checks again, or says why the picked
// one was refused and keeps the folder the card already had.
func (m Model) onFolder(msg folderMsg) (Model, tea.Cmd) {
	if msg.gen != m.gen || m.state != stateFolder {
		return m, nil
	}
	if msg.err != nil || msg.out == "" {
		m.folderErr = folderRefusal(msg.err)
		return m, nil
	}
	m.req.OutDir = msg.out
	// Another folder can be another drive, with another amount of room, so
	// whether keeping the archive is the default is decided afresh.
	m.autoKeep = false
	if !m.modeChosen {
		m.req.Mode = extractengine.ModeInPlace
	}
	return m.startCheck()
}

// folderRefusal is the one sentence shown under the picker for a folder
// Devpit will not extract into.
func folderRefusal(err error) string {
	switch {
	case errors.Is(err, extractengine.ErrNetworkPath):
		return "Network folders are not extracted into. Pick a folder on this PC."
	case errors.Is(err, extractengine.ErrProtected):
		return "Devpit does not extract into that place (system or sign-in files)."
	case errors.Is(err, extractengine.ErrReparsePoint):
		return "That folder is reached through a link. Pick the folder where it really is."
	case errors.Is(err, extractengine.ErrNotAFolder):
		return `That is not a folder Devpit can extract into. Give the full path, like D:\Games.`
	case errors.Is(err, fs.ErrNotExist):
		return "There is no folder at that path."
	case errors.Is(err, fs.ErrPermission):
		return "Windows would not let Devpit look at that folder."
	default:
		return "Devpit cannot extract into that folder. Pick another one."
	}
}

// folderPrompts is what the picker asks, over its list and over the typed
// path, folded to the terminal. Both say the picked folder is the parent.
func (m Model) folderPrompts(ctx uictx.Context) (pick, typ string) {
	fold := func(text string) string { return strings.Join(wrapWords(text, ctx.Width), "\n") }
	return fold("Where should " + m.name + " be extracted? Devpit makes a new folder for it inside the one you pick."),
		fold("Type the folder to extract into, then press Enter. Devpit makes a new folder for " + m.name + " inside it.")
}

// folderPicker is the picker as it is drawn: with its questions folded to
// this terminal.
func (m Model) folderPicker(ctx uictx.Context) pathpicker.Model {
	return m.folder.WithPrompts(m.folderPrompts(ctx))
}

// folderView is the folder picker, with the refusal of the last pick under it
// in the picker's own style.
func (m Model) folderView(ctx uictx.Context) string {
	view := m.folderPicker(ctx).View(ctx)
	if m.folderErr == "" {
		return view
	}
	return view + "\n\n" + strings.Join(styled(ctx.Theme.Danger, hang(ctx.Icons.Fail, m.folderErr, ctx.Width)), "\n")
}

// parentDir is the folder holding path, read with Windows separators whatever
// OS this is built for.
func parentDir(path string) string {
	i := strings.LastIndexAny(path, `\/`)
	switch {
	case i < 0:
		return ""
	case i == 2 && path[1] == ':':
		return path[:3] // the root of a drive keeps its separator
	}
	return path[:i]
}

// otherDrive reports that the files go to another drive than the archive is
// on, which is when extracting in place stops saving space where they land.
func (m Model) otherDrive() bool {
	return m.req.OutDir != "" && !extractengine.SameDrive(m.req.Set.First, m.req.OutDir)
}

// driveName is "E:" for a path on a drive, or "the other drive".
func driveName(path string) string {
	if len(path) >= 2 && path[1] == ':' {
		return strings.ToUpper(path[:1]) + ":"
	}
	return "the other drive"
}

// logLines is "Log: <path>" for the log of a check or a run, or nothing when
// there is no log. It is laid out like every other path, on one line when it
// fits and under its lead when it does not, with one difference: a path too
// long for a line is folded between folders, never cut, because this one is
// for opening.
func logLines(path string, width int) []string {
	const lead = "Log:"
	if path == "" {
		return nil
	}
	if ansi.StringWidth(lead)+1+ansi.StringWidth(path) <= width {
		return []string{lead + " " + path}
	}
	lines := []string{lead}
	for _, l := range wrapWords(path, max(width-2, 8)) {
		lines = append(lines, "  "+l)
	}
	return lines
}

// logPath is the log of what is on screen: the check's on the plan card, the
// run's everywhere after it.
func (m Model) logPath() string {
	switch m.state {
	case stateCheck:
		return m.check.LogPath
	case stateDone, statePaused, stateFailed, stateRestored:
		if m.out.LogPath != "" {
			return m.out.LogPath
		}
		m.op.mu.Lock()
		job := m.op.job
		m.op.mu.Unlock()
		if job != nil {
			return job.LogPath()
		}
	}
	return ""
}
