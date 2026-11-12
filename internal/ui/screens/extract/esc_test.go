package extract

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	extractengine "github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// appEsc presses Esc the way the app does: a screen that is not busy is
// popped before it ever sees the key, a busy one is handed it. It reports
// whether the screen is still on top.
func (r *rig) appEsc() bool {
	r.t.Helper()
	if !uictx.Busy(r.m) {
		return false
	}
	before := r.popped
	r.send(esc())
	return r.popped == before
}

// stay presses Esc and fails if that left the screen.
func (r *rig) stay(where string) *rig {
	r.t.Helper()
	if !r.appEsc() {
		r.t.Fatalf("Esc %s left the Extract screen", where)
	}
	return r
}

// TestEscInTheFolderPickerReturnsToTheCard: Esc closes the picker and nothing
// else. The screen stays on top, on the card it came from, with the archive,
// its folder and its passed check as they were.
func TestEscInTheFolderPickerReturnsToTheCard(t *testing.T) {
	r := newRig(t, scripted(planFits())).toFolder()
	ticket := r.m.ticket

	r.stay("in the folder picker").wantState("check").wantView("in " + zipOut)
	if r.m.req.Set.First != zipPath || r.m.req.OutDir != zipOut {
		t.Errorf("Esc changed the request: archive %q, out %q", r.m.req.Set.First, r.m.req.OutDir)
	}
	if r.m.ticket != ticket || r.ran("check") != 1 {
		t.Errorf("Esc lost the passed check: %d checks", r.ran("check"))
	}
	// The card can go on to the confirmation without another check.
	r.send(enter()).wantState("confirm").wantView("into " + zipOut)
}

// TestEscWhileTypingAFolderGoesBackOneStep: from the typed path to the list,
// then from the list to the card, never out of the screen.
func TestEscWhileTypingAFolderGoesBackOneStep(t *testing.T) {
	r := newRig(t, scripted(planFits())).toFolder()
	r.send(keyCode(tea.KeyDown), keyCode(tea.KeyDown))
	r.send(enter()).typed(`E:\Ga`)
	if !r.m.folder.Typing() {
		t.Fatalf("the folder picker is not typing:\n%s", r.view())
	}

	r.stay("while typing a folder").wantState("folder")
	if r.m.folder.Typing() {
		t.Fatalf("Esc did not leave the typed path:\n%s", r.view())
	}
	r.wantView("Beside the archive", "Type a path…")
	r.stay("in the folder picker").wantState("check")
	if r.m.req.OutDir != zipOut {
		t.Errorf("the folder is %q", r.m.req.OutDir)
	}
}

// TestEscWhileTypingAnArchiveGoesBackToTheList: the typed path gives way to
// the list of archives. Esc on the list itself is the one that leaves.
func TestEscWhileTypingAnArchiveGoesBackToTheList(t *testing.T) {
	r := typeRow(newRig(t, twoFound()))
	r.typed(`D:\Downloads\da`)

	r.stay("while typing an archive").wantState("pick")
	if r.m.picker.Typing() {
		t.Fatalf("Esc did not leave the typed path:\n%s", r.view())
	}
	r.wantView(zipName)
	if r.appEsc() {
		t.Error("Esc on the archive list did not leave the screen")
	}
}

// TestEscInThePasswordPromptGoesBack: to the archive list, with what was
// typed thrown away.
func TestEscInThePasswordPromptGoesBack(t *testing.T) {
	r := newRig(t, scripted(needsPassword()))
	r.start().send(enter()).until("password").typed(typedPw)

	r.stay("in the password prompt").wantState("pick").wantView(zipName)
	if r.m.pass.Value() != "" {
		t.Error("the typed password outlived Esc")
	}
}

// TestEscClosesTheDetailsOnly: the details view sits over a card, and Esc
// takes it off again.
func TestEscClosesTheDetailsOnly(t *testing.T) {
	r := newRig(t, scripted(planFits())).toCheck()
	r.key("l")
	r.stay("in the details view").wantState("check")
	if r.m.details {
		t.Error("Esc left the details view open")
	}
}

// TestAStepThatKeepsEscNeverHoldsUpQuitting: Ctrl+C asks a busy screen to
// stop and waits for it. A picker has nothing to wait for.
func TestAStepThatKeepsEscNeverHoldsUpQuitting(t *testing.T) {
	r := newRig(t, scripted(planFits())).toFolder()
	if !r.m.Busy() {
		t.Fatal("the folder picker does not keep Esc")
	}
	r.m.Stop()
	if r.m.Busy() {
		t.Error("a stopped screen still reports work in flight")
	}
}

// TestTheFolderPickerOffersTheArchivesFolderFirst: the folder the archive is
// in leads the list, highlighted, ahead of the projects folder, and is never
// listed twice.
func TestTheFolderPickerOffersTheArchivesFolderFirst(t *testing.T) {
	var picked string
	r := newRig(t, scripted(planFits()), with(WithOutDirIn(func(parent string, set archive.Set) (string, error) {
		picked = parent
		return fakeOutDirIn(parent, set)
	}))).toCheck()
	// An archive at the root of one drive, a projects folder on another.
	r.m.req.Set.First = `E:\Red-Dead-Redemption-2-AnkerGames.zip`
	r.m.projects = `D:\`
	for _, dir := range []string{`E:\`, `D:\`} {
		r.fakes.FS.AddDir(dir)
	}
	r.key("o").wantState("folder")

	view := r.view()
	first, def := strings.Index(view, "Beside the archive"), strings.Index(view, "Your default projects folder")
	if first < 0 || def < 0 || first > def {
		t.Fatalf("the archive's folder does not lead the list:\n%s", view)
	}
	if !strings.Contains(view, `▸ E:\`) {
		t.Fatalf("the archive's folder is not the highlighted row:\n%s", view)
	}

	// Enter takes it, as the parent of the new folder.
	r.send(enter())
	if picked != `E:\` {
		t.Errorf("Enter on the first row picked %q, want the archive's folder", picked)
	}

	// The projects folder being the archive's own is one row, not two.
	same := newRig(t, scripted(planFits())).toCheck()
	same.m.projects = `d:\downloads`
	same.key("o").wantState("folder").wantView(`▸ D:\Downloads`, "Beside the archive")
	if strings.Contains(same.view(), "Your default projects folder") {
		t.Errorf("one folder is listed twice:\n%s", same.view())
	}
}

// protectedPlaces makes the rig's Identify refuse an archive under Windows'
// own folder the way the engine does (rule 82), reason and all.
func protectedPlaces() rigOpt {
	return func(s *rigSetup) {
		s.opts = append(s.opts, func(m *Model) {
			inner := m.identify
			m.identify = func(path string) (archive.Set, error) {
				if strings.HasPrefix(strings.ToLower(path), strings.ToLower(sysParent)) {
					return archive.Set{}, fmt.Errorf("%w: %s holds Windows or installed programs", extractengine.ErrProtected, sysParent)
				}
				return inner(path)
			}
		})
	}
}

// TestAProtectedArchiveIsRefusedInOneSentence: an archive in a place Devpit
// does not extract from is refused under the typed path, which stays for
// editing, and nothing is run.
func TestAProtectedArchiveIsRefusedInOneSentence(t *testing.T) {
	r := typeRow(newRig(t, twoFound(), protectedPlaces()))
	r.typed(sysParent + `\Temp\big.zip`).send(enter()).wantState("pick")
	r.wantView("✗ Devpit does not extract archives in that place (system or sign-in files).", `\Temp\big.zip`)
	if !r.m.picker.Typing() {
		t.Errorf("the typed path was taken away:\n%s", r.view())
	}
	if strings.Contains(r.view(), "holds Windows") || r.ran("check") != 0 {
		t.Errorf("the refusal shows error plumbing, or something ran:\n%s", r.view())
	}
}

// TestAFastTypedPathKeepsItsDoubledLetters: a pasted path arrives a key at a
// time with no gap between them. The debounce that drops a console's echoed
// Enter must not drop the second p of AppData, or the path is one that does
// not exist. Off the text field it still drops an echo.
func TestAFastTypedPathKeepsItsDoubledLetters(t *testing.T) {
	var (
		mu     sync.Mutex
		frozen atomic.Bool
	)
	step := stepClock()
	at := step()
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		if !frozen.Load() {
			at = step()
		}
		return at
	}
	r := typeRow(newRig(t, twoFound(), with(WithClock(clock))))
	frozen.Store(true)
	r.typed(`C:\Users\zz\AppData\Local\Temp\t.zip`)
	r.wantView(`› C:\Users\zz\AppData\Local\Temp\t.zip`)

	// The password field is a text field too.
	frozen.Store(false)
	p := newRig(t, scripted(needsPassword()), with(WithClock(clock)))
	p.start().send(enter()).until("password")
	frozen.Store(true)
	p.typed("oooo")
	if got := p.m.pass.Value(); got != "oooo" {
		t.Errorf("the password field holds %d characters, want 4", len(got))
	}

	// On a card a doubled key is still an echo: l opens the details, and
	// its echo does not close them again.
	frozen.Store(false)
	c := newRig(t, scripted(planFits()), with(WithClock(clock))).toCheck()
	frozen.Store(true)
	c.key("l").key("l")
	if !c.m.details {
		t.Error("an echoed key on a card was acted on twice")
	}
}
