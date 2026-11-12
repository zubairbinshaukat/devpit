package extract

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	extractengine "github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The screen's own keys. Letters only act on the cards that show them, and
// never while a text field has the keyboard.
const (
	keyEnter   = "enter"
	keyEsc     = "esc"
	keyKeep    = "k"
	keyInPlace = "i"
	keyFolder  = "o"
	keyDetails = "l"
	keyRetry   = "r"
	keySkip    = "s"
)

// onKey routes a key press to whatever owns the keyboard right now.
func (m Model) onKey(km tea.KeyPressMsg) (Model, tea.Cmd) {
	k := km.String()

	// The details view sits over a card; l closes it, as it opened it.
	if m.details {
		if k == keyDetails || k == keyEsc || k == keyEnter {
			m.details = false
		}
		return m, nil
	}

	switch m.state {
	case statePick:
		next, cmd := m.picker.Update(km)
		m.picker = next
		return m, cmd

	case stateTool:
		if k == keyRetry {
			m.toolOK = false
			return m.detectTool()
		}
		if k == keyEsc {
			return m.back()
		}
		next, cmd := m.menu.Update(km)
		m.menu = next
		return m, cmd

	case stateUnfinished:
		if k == keyEsc {
			return m.back()
		}
		next, cmd := m.menu.Update(km)
		m.menu = next
		return m, cmd

	case stateToolConfirm, stateConfirm, stateRestoreConfirm:
		next, cmd := m.dialog.Update(km)
		m.dialog = next
		return m, cmd

	case stateInstalling:
		switch k {
		case keySkip:
			return m.skipStep()
		case keyEsc:
			m.op.stop()
			return m, uictx.Status("warning", "Stopping the install…")
		}
		// Any other key means the user changed their mind about skipping.
		m.inst.gate = m.inst.gate.Disarm()
		return m, nil

	case stateInstallFailed:
		switch k {
		case keyRetry, keyEsc, keyEnter:
			// Back to the offer: installing again needs a new Yes.
			m.state = stateTool
			m.tool.note = ""
		case keyDetails:
			m.details = true
		}
		return m, nil

	case stateCheck:
		return m.onCheckKey(k)

	case stateFolder:
		// The folder picker has the keyboard, typed path included.
		m.folderErr = ""
		next, cmd := m.folder.Update(km)
		m.folder = next
		return m, cmd

	case statePassword:
		switch k {
		case keyEnter:
			return m.submitPassword()
		case keyEsc:
			m.pass.SetValue("")
			return m.back()
		}
		var cmd tea.Cmd
		m.pass, cmd = m.pass.Update(km)
		m.wrongPass = false
		return m, cmd

	case stateRunning:
		if k == keyEsc {
			return m.pause()
		}
		return m, nil

	case statePaused:
		switch k {
		case keyEnter, keyRetry:
			// Resuming is a new run: the check and the confirmation again.
			m.resuming = true
			return m.proceed(actCheck)
		case keyDetails:
			m.details = true
		case keyEsc:
			return m, uictx.Pop()
		}
		return m, nil

	case stateFailed:
		switch k {
		case keyRetry:
			return m.proceed(actCheck)
		case keyKeep:
			if m.out.Kind == extractengine.KindSolid {
				// Nothing of a solid RAR was extracted in place, so this is
				// a fresh extraction that keeps the archive.
				m.resuming = false
				return m.switchMode(extractengine.ModeKeepArchive)
			}
		case keyDetails:
			m.details = true
		case keyEsc:
			return m, uictx.Pop()
		}
		return m, nil

	case stateRestoring:
		if k == keyEsc {
			m.op.stop()
			return m, uictx.Status("warning", "Stopping the restore…")
		}
		return m, nil

	case stateRestored:
		switch k {
		case keyDetails:
			if m.restoreErr != "" {
				m.details = true
			}
		case keyRetry:
			if m.restoreErr != "" {
				m.state = stateUnfinished
			}
		case keyEnter, keyEsc:
			return m, uictx.Pop()
		}
		return m, nil

	case stateDone:
		if k == keyEnter || k == keyEsc {
			return m, uictx.Pop()
		}
		return m, nil

	case stateChecking:
		if k == keyEsc {
			return m, uictx.Pop()
		}
	}
	return m, nil
}

// onCheckKey handles the plan card. Enter only continues from a check that
// passed, and it continues to the confirmation, never to the run.
func (m Model) onCheckKey(k string) (Model, tea.Cmd) {
	fits := m.check.Verdict == extractengine.VerdictFits
	switch k {
	case keyEnter:
		if fits {
			return m.askRun()
		}
	case keyKeep:
		if m.offersKeep() {
			return m.switchMode(extractengine.ModeKeepArchive)
		}
	case keyInPlace:
		if m.offersInPlace() {
			return m.switchMode(extractengine.ModeInPlace)
		}
	case keyFolder:
		return m.openFolder()
	case keyRetry:
		return m.startCheck()
	case keyDetails:
		m.details = true
	case keyEsc:
		return m.back()
	}
	return m, nil
}

// offersKeep reports whether the card offers "keep the archive instead".
func (m Model) offersKeep() bool {
	if !m.canSwitchMode() && m.check.Verdict != extractengine.VerdictSolid {
		return false
	}
	if m.req.Mode != extractengine.ModeInPlace || m.check.Plan.AllDone {
		return false
	}
	switch m.check.Verdict {
	case extractengine.VerdictFits, extractengine.VerdictSolid:
		return true
	}
	return false
}

// offersInPlace reports whether the card offers "extract in place instead".
func (m Model) offersInPlace() bool {
	if !m.canSwitchMode() || m.req.Mode != extractengine.ModeKeepArchive || m.check.Plan.AllDone {
		return false
	}
	switch m.check.Verdict {
	case extractengine.VerdictFits, extractengine.VerdictNoSpace:
		return true
	}
	return false
}

// ShortHelp implements uictx.Screen: the key hints of the step on screen.
func (m Model) ShortHelp() []key.Binding {
	esc := bind(keyEsc, "back")
	if m.details {
		return []key.Binding{bind(keyDetails, "close details"), esc}
	}
	switch m.state {
	case statePick:
		if m.picker.Typing() {
			return []key.Binding{bind(keyEnter, "choose"), esc}
		}
		return m.picker.Keys.ShortHelp()

	case stateTool:
		if !m.tool.scoop {
			return []key.Binding{bind(keyRetry, "check again"), esc}
		}
		return []key.Binding{m.menu.Keys.Up, m.menu.Keys.Select, bind(keyRetry, "check again"), esc}

	case stateUnfinished:
		return []key.Binding{m.menu.Keys.Up, m.menu.Keys.Select, esc}

	case stateToolConfirm, stateConfirm, stateRestoreConfirm:
		return m.dialog.Keys.ShortHelp()

	case stateInstalling:
		skip := bind(keySkip, "skip")
		if m.inst.gate.Armed(m.inst.idx, m.now()) {
			skip = bind(keySkip, "press s again to skip")
		}
		return []key.Binding{skip, bind(keyEsc, "stop")}

	case stateInstallFailed:
		return []key.Binding{bind(keyRetry, "try again"), bind(keyDetails, "details"), esc}

	case stateCheck:
		return m.checkHelp(false)

	case stateFolder:
		return m.folder.Keys.ShortHelp()

	case statePassword:
		return []key.Binding{bind(keyEnter, "continue"), esc}

	case stateRunning:
		if m.pausing {
			return []key.Binding{bind(keyEsc, "pausing…")}
		}
		return []key.Binding{bind(keyEsc, "pause (resume later)")}

	case statePaused:
		return []key.Binding{bind(keyEnter, "resume now"), esc}

	case stateFailed:
		hints := []key.Binding{bind(keyRetry, "try again")}
		if m.out.Kind == extractengine.KindSolid {
			hints = []key.Binding{bind(keyKeep, "extract and keep the archive")}
		}
		return append(hints, bind(keyDetails, "details"), esc)

	case stateRestoring:
		return []key.Binding{bind(keyEsc, "stop")}

	case stateRestored:
		if m.restoreErr != "" {
			return []key.Binding{bind(keyRetry, "back to resume or restore"), bind(keyDetails, "details"), esc}
		}
		return []key.Binding{bind(keyEnter, "back")}

	case stateDone:
		return []key.Binding{bind(keyEnter, "back")}

	default:
		return []key.Binding{esc}
	}
}

// checkHelp is the key hints of the plan card, which depend on how the check
// went. The footer of an 80 column terminal holds four hints beside the app's
// own "? help", so when the card offers both a mode and a folder the footer
// leaves details to the help overlay; all lists every key.
func (m Model) checkHelp(all bool) []key.Binding {
	var hints []key.Binding
	if m.check.Verdict == extractengine.VerdictFits {
		hints = append(hints, bind(keyEnter, "continue"))
	} else {
		hints = append(hints, bind(keyRetry, "check again"))
	}
	mode := false
	if m.offersKeep() {
		hints, mode = append(hints, bind(keyKeep, "keep archive")), true
	}
	if m.offersInPlace() {
		hints, mode = append(hints, bind(keyInPlace, "in place")), true
	}
	folder := m.offersFolder()
	if folder {
		hints = append(hints, bind(keyFolder, "change folder"))
	}
	if all || !mode || !folder {
		hints = append(hints, bind(keyDetails, "details"))
	}
	return append(hints, bind(keyEsc, "back"))
}

// FullHelp implements uictx.Screen: the footer's hints, and on the plan card
// every key it answers to.
func (m Model) FullHelp() [][]key.Binding {
	if m.state == stateCheck && !m.details {
		return [][]key.Binding{m.checkHelp(true)}
	}
	return [][]key.Binding{m.ShortHelp()}
}
