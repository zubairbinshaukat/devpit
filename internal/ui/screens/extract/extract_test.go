package extract

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	extractengine "github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/extract/extracttest"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
)

// ran reports how many hatchzip runs began with the given word (check,
// extract or restore).
func (r *rig) ran(word string) int {
	n := 0
	for _, s := range r.fakes.Runner.Specs() {
		if len(s.Args) > 0 && s.Args[0] == word {
			n++
		}
	}
	return n
}

// lastSpec is the newest hatchzip run.
func (r *rig) lastSpec() extractengine.Spec {
	r.t.Helper()
	specs := r.fakes.Runner.Specs()
	if len(specs) == 0 {
		r.t.Fatal("hatchzip was never run")
	}
	return specs[len(specs)-1]
}

// TestCannotReachRunningWithoutConfirm is safety rule 76 at the screen: an
// extraction starts only from a Yes that this screen's own run dialog
// produced, after a check that passed. No key on the plan card, no stray or
// forged answer, and no second use of a ticket gets there.
func TestCannotReachRunningWithoutConfirm(t *testing.T) {
	r := newRig(t, scripted(planFits(), extractHangs(), planResuming()))
	r.toCheck()

	// Every key the plan card might see, short of walking the dialog.
	for _, k := range []string{"y", "Y", "x", "d", " ", "s", "l", "l"} {
		r.key(k)
	}
	// Answers that are not this screen's run dialog answering Yes right now:
	// another dialog's Yes, a No, and a Yes while no dialog is up.
	r.send(
		confirm.AnsweredMsg{ID: "clean.delete", Answer: confirm.AnswerYes},
		confirm.AnsweredMsg{ID: installDialogID, Answer: confirm.AnswerYes},
		confirm.AnsweredMsg{ID: restoreDialogID, Answer: confirm.AnswerYes},
		confirm.AnsweredMsg{ID: runDialogID, Answer: confirm.AnswerNo},
		confirm.AnsweredMsg{ID: runDialogID, Answer: confirm.AnswerYes},
	)
	if r.state() == "running" || r.ran("extract") != 0 {
		t.Fatalf("an extraction started without the confirmation (state %q, %d extract runs)", r.state(), r.ran("extract"))
	}

	// Enter opens the dialog; it does not answer it.
	r.send(enter()).wantState("confirm")
	if r.ran("extract") != 0 {
		t.Fatal("opening the confirmation ran hatchzip extract")
	}
	if r.yes[runDialogID] {
		t.Fatal("a Yes was recorded before the dialog was answered")
	}

	// The dialog's own Yes is the one way in.
	r.key("y").wantState("running")
	if !r.yes[runDialogID] {
		t.Fatal("the screen is running, but its dialog never produced a Yes")
	}
	r.untilFunc("hatchzip extract started", func(Model) bool { return r.ran("extract") == 1 })
	if args := r.lastSpec().Args; !contains(args, "-y") || args[0] != "extract" {
		t.Errorf("the run's argv is %v", args)
	}

	// The ticket is spent. After the pause, a forged Yes starts nothing: the
	// screen must check and ask again.
	r.send(esc()).until("paused")
	r.send(confirm.AnsweredMsg{ID: runDialogID, Answer: confirm.AnswerYes})
	if r.state() == "running" || r.ran("extract") != 1 {
		t.Fatalf("a used ticket started a second extraction (state %q)", r.state())
	}
	r.send(enter()).until("check")
	if r.ran("check") != 2 {
		t.Errorf("resuming ran %d checks in all, want a fresh one", r.ran("check"))
	}
	if r.m.ticket == nil {
		t.Error("the fresh check left no ticket")
	}
}

// TestNoTicketMeansNoRun covers the other half of the guard: with the dialog
// answering Yes but no passed check behind it, nothing starts.
func TestNoTicketMeansNoRun(t *testing.T) {
	r := newRig(t, scripted(planFits())).toConfirm()
	r.m.ticket = nil
	r.key("y")
	if r.state() == "running" || r.ran("extract") != 0 {
		t.Fatalf("a run started without a ticket (state %q)", r.state())
	}
}

// TestACheckThatDoesNotPassOffersNoRun: no space, damage and a bad argument
// leave no ticket and no way to the confirmation (rule 77, seen from here).
func TestACheckThatDoesNotPassOffersNoRun(t *testing.T) {
	for name, script := range map[string]extracttest.Script{
		"no space": planNoFit(),
		"damaged":  extracttest.Check(4, "", "error: unexpected end of archive"),
		"bad args": extracttest.Check(2, "", "error: unexpected argument"),
		"unknown":  extracttest.Check(7, "", "error: something new"),
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, scripted(script)).toCheck()
			r.send(enter()).key("y")
			r.send(confirm.AnsweredMsg{ID: runDialogID, Answer: confirm.AnswerYes})
			if r.state() != "check" || r.m.ticket != nil || r.ran("extract") != 0 {
				t.Fatalf("state %q, ticket %v, %d extract runs", r.state(), r.m.ticket != nil, r.ran("extract"))
			}
			for _, hint := range r.m.ShortHelp() {
				if hint.Help().Desc == "continue" {
					t.Error("the card offers Enter to continue")
				}
			}
		})
	}
}

func TestEnterOnTheRunConfirmAnswersNo(t *testing.T) {
	r := newRig(t, scripted(planFits())).toConfirm()
	if got := r.m.dialog.Answer(); got != confirm.AnswerNo {
		t.Fatalf("a fresh run dialog would answer %v", got)
	}
	r.send(enter())
	r.wantState("check")
	if r.ran("extract") != 0 || r.yes[runDialogID] {
		t.Fatal("Enter on a fresh dialog started the extraction")
	}
	if len(r.status) == 0 || r.status[len(r.status)-1] != "Nothing was extracted." {
		t.Errorf("status = %v", r.status)
	}
	// Esc is No as well.
	r.send(enter()).wantState("confirm").send(esc()).wantState("check")
	if r.ran("extract") != 0 {
		t.Fatal("Esc on the dialog started the extraction")
	}
}

func TestInPlaceConfirmSaysTheArchiveIsRemoved(t *testing.T) {
	r := newRig(t, scripted(planFits())).toConfirm()
	r.wantView(
		"Extract "+zipName+" in place and remove the archive?",
		zipOut,
		"cut the archive down as each part is written",
		zipName+" is gone",
		"Keep another copy if this archive is the only one.",
		"Esc pauses safely",
		"[ No ]",
	)
	if !strings.Contains(r.m.dialog.Question, zipName) || !strings.Contains(r.m.dialog.Question, "remove") {
		t.Errorf("the question is %q", r.m.dialog.Question)
	}

	// Keeping the archive asks a different question, and says nothing is
	// removed.
	k := newRig(t, scripted(planFits(), planKeep())).toCheck()
	k.key("k").until("check").send(enter()).wantState("confirm")
	k.wantView("Extract "+zipName+" and keep the archive?", "nothing is removed")
	if strings.Contains(k.view(), "is gone") {
		t.Errorf("the keep-archive question says the archive goes:\n%s", k.view())
	}
}

func TestThePasswordFieldIsMaskedAndEmptiedAfterSubmit(t *testing.T) {
	needed := needsPassword()
	r := newRig(t, scripted(needed, planFits()))
	r.start().send(enter()).until("password")
	r.wantView(zipName + " is protected with a password.")

	r.typed(typedPw)
	if v := r.view(); strings.Contains(v, typedPw) || !strings.Contains(v, strings.Repeat("*", len(typedPw))) {
		t.Fatalf("the field is not masked:\n%s", v)
	}

	r.send(enter()).until("check")
	if got := r.m.pass.Value(); got != "" {
		t.Errorf("the field still holds %q after submit", got)
	}
	// The password went to the second check through the spec's secret, and
	// nowhere near its argv.
	spec := r.lastSpec()
	if spec.Password.Empty() {
		t.Error("the check after the password carried none")
	}
	if strings.Contains(strings.Join(spec.Args, " "), typedPw) {
		t.Errorf("the password is in the argv: %v", spec.Args)
	}
	if first := r.fakes.Runner.Specs()[0]; !first.Password.Empty() {
		t.Error("the first check carried a password nobody had typed")
	}

	// A wrong password empties the field again and says so.
	w := newRig(t, scripted(needed, wrongPassword()))
	w.start().send(enter()).until("password").typed(typedPw).send(enter()).until("password")
	w.wantView("That password did not open the archive.")
	if got := w.m.pass.Value(); got != "" {
		t.Errorf("the field holds %q after a wrong password", got)
	}
	if !w.m.secret.Empty() {
		t.Error("a refused password is still held")
	}
}

// TestCtrlCPausesARunningExtraction is rule 80 at the screen: the app's Stop
// (Ctrl+C, or the screen being discarded) pauses hatchzip with Ctrl+Break. It
// returns at once, nothing is killed, and the screen stops being busy inside
// the engine's grace periods.
func TestCtrlCPausesARunningExtraction(t *testing.T) {
	r := newRig(t, scripted(planFits(), extractHangs())).toRunning()
	r.untilFunc("hatchzip extract started", func(Model) bool { return r.ran("extract") == 1 })
	if !r.m.Busy() {
		t.Fatal("a running extraction is not busy")
	}

	began := time.Now()
	r.m.Stop()
	if took := time.Since(began); took > 500*time.Millisecond {
		t.Fatalf("Stop blocked for %s", took)
	}
	r.m.Stop() // twice is safe

	fakeStart := r.fakes.Clock.Now()
	r.until("paused")
	if r.m.Busy() {
		t.Error("the screen is still busy after the pause")
	}
	if n := r.fakes.Runner.Interrupts(); n < 1 {
		t.Errorf("hatchzip got %d Ctrl+Breaks, want at least one", n)
	}
	if n := r.fakes.Runner.Kills(); n != 0 {
		t.Errorf("hatchzip was killed %d times; a pause never kills first", n)
	}
	if waited := r.fakes.Clock.Now().Sub(fakeStart); waited > extractengine.DefaultPauseGrace+extractengine.DefaultBreakGrace {
		t.Errorf("the pause took %s of engine time, past the grace periods", waited)
	}
	if r.m.out.Kind != extractengine.KindPaused {
		t.Errorf("the run ended as %v, want paused", r.m.out.Kind)
	}
}

// TestAHatchzipThatWillNotPauseIsStoppedNotFailed: when both Ctrl+Breaks are
// ignored the engine ends the job, and the screen says stopped and resumable.
func TestAHatchzipThatWillNotPauseIsStoppedNotFailed(t *testing.T) {
	stubborn := extractHangs()
	stubborn.IgnoreBreaks = 2
	r := newRig(t, scripted(planFits(), stubborn)).toRunning()
	r.untilFunc("hatchzip extract started", func(Model) bool { return r.ran("extract") == 1 })
	r.send(esc()).until("paused")
	if r.m.out.Kind != extractengine.KindStopped {
		t.Fatalf("the run ended as %v, want stopped", r.m.out.Kind)
	}
	r.wantView("Stopped.", "Devpit stopped it", "Progress is saved")
	if strings.Contains(r.view(), "failed") {
		t.Errorf("a stop Devpit caused reads as a failure:\n%s", r.view())
	}
}

func TestEscDuringRunPausesAndShowsPaused(t *testing.T) {
	// This hatchzip takes its time over the first Ctrl+Break, as it does in
	// the middle of a big entry, so the pausing frame can be seen.
	slow := extractHangs()
	slow.IgnoreBreaks = 1
	r := newRig(t, scripted(planFits(), slow, planResuming())).toRunning()
	r.untilFunc("the first part", func(m Model) bool { return m.prog.DoneBytes > 0 })
	r.wantView("Extracting", "in place", "about 20.0 GB of 96.0 GB", "Archive", "now 64.2 GB, was 84.2 GB", "Esc pauses safely")

	r.send(esc())
	r.wantState("running").wantView("Pausing", "hatchzip is finishing the part it is on")
	r.send(esc(), esc()) // more Esc changes nothing

	r.until("paused")
	r.wantView("Paused.", "extracted so far", zipOut, zipName+" now holds only what is left")
	if n := r.fakes.Runner.Kills(); n != 0 {
		t.Errorf("Esc killed hatchzip %d times", n)
	}

	// Enter resumes: a fresh check first, and the confirmation again.
	r.send(enter()).until("check")
	r.wantView("Resume "+zipName, "Resuming: about 39.4 GB of 96.0 GB left")
	r.send(enter()).wantState("confirm").wantView("Resume extracting " + zipName)
	if r.ran("extract") != 1 {
		t.Errorf("%d extract runs before the resume was confirmed", r.ran("extract"))
	}
}

func TestARunThatFinishesShowsDone(t *testing.T) {
	r := newRig(t, scripted(planFits(), extractAll())).toRunning()
	var last int64
	r.untilFunc("the end", func(m Model) bool {
		if m.state == stateRunning {
			if m.prog.DoneBytes < last {
				t.Errorf("the bar went backwards: %d after %d", m.prog.DoneBytes, last)
			}
			last = m.prog.DoneBytes
			if p := m.percent(); p > 99 {
				t.Errorf("the bar shows %d%% before hatchzip exited", p)
			}
		}
		return m.state != stateRunning
	})
	r.wantState("done").wantView("Done.", "about 96.0 GB extracted", "12,408 items", "The files are in "+zipOut, zipName+" was removed.", "Free on D: about 15.7 GB")
	if r.m.Busy() {
		t.Error("a finished run is busy")
	}
	if !r.m.secret.Empty() {
		t.Error("the password outlived the run")
	}
	r.send(enter())
	if r.popped != 1 {
		t.Errorf("Enter on the done card popped %d screens", r.popped)
	}
}

// sizeRe finds a size as hatchzip and Devpit print one.
var sizeRe = regexp.MustCompile(`\d+(?:\.\d)? (?:[KMGT]B|B)\b`)

// TestEverySizeOnTheCheckCardSaysAbout is rule 84 on the card: every size on
// it came from hatchzip's rounded text, so every one says "about" (the second
// size of an "about X of Y" pair is covered by the first).
func TestEverySizeOnTheCheckCardSaysAbout(t *testing.T) {
	cards := map[string]*rig{
		"fits":     newRig(t, scripted(planFits())),
		"keep":     newRig(t, scripted(planNote(keepNote), planKeep())),
		"no fit":   newRig(t, scripted(planNoFit())),
		"resuming": unfinished(newRig(t, scripted(planResuming()))),
	}
	for name, r := range cards {
		r.start().send(enter())
		if name == "resuming" {
			r.send(enter())
		}
		r.until("check")
		card := r.view()
		found := sizeRe.FindAllStringIndex(card, -1)
		if len(found) < 3 {
			t.Fatalf("%s: only %d sizes on the card:\n%s", name, len(found), card)
		}
		for _, loc := range found {
			before := card[:loc[0]]
			if strings.HasSuffix(before, "about ") {
				continue
			}
			if i := strings.LastIndex(before, "about "); i >= 0 && sizeRe.MatchString(before[i:]) && strings.HasSuffix(before, " of ") {
				continue
			}
			t.Errorf("%s: the size %q does not say about:\n%s", name, card[loc[0]:loc[1]], card)
		}
	}
}

// TestTheVerdictComesFromTheExitCodeNotTheText: the same reassuring plan text
// with exit 3 is a no-fit card, and an alarming line with exit 0 still fits.
func TestTheVerdictComesFromTheExitCodeNotTheText(t *testing.T) {
	bad := newRig(t, scripted(extracttest.Check(3, extracttest.SamplePlan()))).toCheck()
	bad.wantView("Not enough space")
	if strings.Contains(bad.view(), "Fits") {
		t.Errorf("exit 3 drew a tick:\n%s", bad.view())
	}
	good := newRig(t, scripted(extracttest.Check(0, extracttest.SamplePlan()+"Insufficient disk space: 4.2 GB more is required\nsomething new and unknown\n"))).toCheck()
	good.wantView("Fits in place")
	if good.m.ticket == nil {
		t.Error("exit 0 left no ticket")
	}
}

func TestKSwitchesToKeepArchiveAndChecksAgain(t *testing.T) {
	r := newRig(t, scripted(planFits(), planKeep(), planFits())).toCheck()
	r.wantView("Fits in place", "In place needs")
	if contains(r.lastSpec().Args, "--keep-archive") {
		t.Fatalf("the first check kept the archive: %v", r.lastSpec().Args)
	}

	r.key("k").until("check")
	if r.ran("check") != 2 || !contains(r.lastSpec().Args, "--keep-archive") {
		t.Fatalf("k did not check again with --keep-archive: %v", r.lastSpec().Args)
	}
	r.wantView("The archive is kept and nothing is removed.", "Keeping the archive needs")
	if r.m.req.Mode != extractengine.ModeKeepArchive || r.m.ticket == nil {
		t.Error("the keep-archive check left no ticket of its own")
	}

	// i goes back, with a check of its own again.
	r.key("i").until("check")
	if r.ran("check") != 3 || contains(r.lastSpec().Args, "--keep-archive") {
		t.Fatalf("i did not check again in place: %v", r.lastSpec().Args)
	}
	r.wantView("Fits in place")
}

func TestKeepArchiveIsTheDefaultWhenItFits(t *testing.T) {
	r := newRig(t, scripted(planNote(keepNote), planKeep(), planFits())).toCheck()
	if r.m.req.Mode != extractengine.ModeKeepArchive {
		t.Fatalf("with room to keep the archive, the mode is %v", r.m.req.Mode)
	}
	if r.ran("check") != 2 || !contains(r.lastSpec().Args, "--keep-archive") {
		t.Fatalf("keeping the archive was not checked for: %v", r.lastSpec().Args)
	}
	r.wantView("There is room to keep the archive", "nothing is removed", "Press i to extract in place instead")

	// The confirmation asks the keep question, and i still gets in place.
	r.send(enter()).wantState("confirm").wantView("and keep the archive?")
	r.send(esc()).wantState("check").key("i").until("check")
	r.wantView("Fits in place")
	if r.m.req.Mode != extractengine.ModeInPlace {
		t.Error("i did not switch to in place")
	}
}

func TestSolidOffersKeepArchive(t *testing.T) {
	solid := extracttest.Check(1, "", errSolid)
	r := newRig(t, scripted(solid, planKeep())).toCheck()
	r.wantView("This RAR is solid, so it cannot be extracted in place. Nothing was removed.", "Press k to extract it and keep the archive")
	if r.m.ticket != nil {
		t.Fatal("a refused check left a ticket")
	}
	r.key("k").until("check")
	if !contains(r.lastSpec().Args, "--keep-archive") {
		t.Fatalf("k did not check with --keep-archive: %v", r.lastSpec().Args)
	}
	r.wantView("The archive is kept and nothing is removed.")

	// The same card when hatchzip only says so once the run has started.
	late := newRig(t, scripted(planFits(), extracttest.Script{Exit: 1, Stderr: errSolid + "\n"}, planKeep())).toConfirm()
	late.key("y").until("failed").wantView("This RAR is solid", "Press k to extract it and keep the archive")
	late.key("k").until("check")
	if !contains(late.lastSpec().Args, "--keep-archive") {
		t.Fatalf("k after a solid run did not check with --keep-archive: %v", late.lastSpec().Args)
	}
}

// TestWithArchiveStartsAtTheCheck is rule 89 from this side: a screen opened
// on an archive goes to its check, and stops there.
func TestWithArchiveStartsAtTheCheck(t *testing.T) {
	r := newRig(t, with(WithArchive(zipPath)), scripted(planFits()))
	if r.state() != "checking" {
		t.Fatalf("before its first message the screen is on %q", r.state())
	}
	r.start().until("check")
	r.wantView("Extract "+zipName, "Fits in place")
	if r.ran("check") != 1 || r.ran("extract") != 0 {
		t.Fatalf("%d checks and %d extract runs", r.ran("check"), r.ran("extract"))
	}
	if r.m.Busy() {
		t.Error("the check card is busy")
	}

	// A path that is refused lands on the picker with the reason.
	bad := newRig(t, with(WithArchive(`\\nas\backups\big.zip`)))
	bad.start().until("pick")
	bad.wantView("Network paths are not extracted. Copy the archive to this PC first.")
	if len(bad.fakes.Runner.Specs()) != 0 {
		t.Error("hatchzip was run for a refused path")
	}

	// An unfinished one opens on Resume or Restore.
	u := unfinished(newRig(t, with(WithArchive(zipPath))))
	u.start().until("unfinished")
}

func TestWithArchivesListsOnlyThose(t *testing.T) {
	r := newRig(t, with(WithArchives([]archive.Set{zipSet(), datasetSet()})), scripted(planFits()))
	r.start()
	r.wantState("pick").wantView(zipPath, `dataset.7z.001  (+10 parts)`)
	for _, hidden := range []string{"Type a path", "Browse"} {
		if strings.Contains(r.view(), hidden) {
			t.Errorf("a limited picker offers %q:\n%s", hidden, r.view())
		}
	}
	r.send(enter()).until("check")
}

func TestAnUnfinishedArchiveOpensResumeOrRestore(t *testing.T) {
	r := unfinished(newRig(t, scripted(planResuming())))
	r.start()
	r.wantView("Unfinished · 34.0 GB · started 2 days ago")
	r.send(enter()).wantState("unfinished")
	r.wantView(zipName+" has an unfinished extraction, started 2 days ago.", zipOut, "Resume", "Restore the archive", "Leave it for now")
	if len(r.fakes.Runner.Specs()) != 0 {
		t.Fatal("hatchzip ran before Resume or Restore was chosen")
	}

	// Resume goes to the check, with the folder Devpit recorded.
	r.send(enter()).until("check")
	if args := r.lastSpec().Args; args[0] != "check" || !contains(args, zipOut) {
		t.Errorf("the resume check's argv is %v", args)
	}
	r.wantView("Resume "+zipName, "Resuming: about 39.4 GB of 96.0 GB left")
	// A resumed extraction carries on the way it began: no mode switch.
	r.key("k")
	if r.ran("check") != 1 {
		t.Error("k switched the mode of a resumed extraction")
	}

	// A journal with no record of Devpit's is unfinished as well.
	j := newRig(t)
	j.fakes.FS.AddFile(zipPath+".hatchzip", 4096)
	j.start().send(enter()).wantState("unfinished")

	// Leaving it changes nothing: the record stays, nothing is run.
	l := unfinished(newRig(t))
	l.start().send(enter(), keyCode(tea.KeyDown), keyCode(tea.KeyDown), enter())
	if l.popped != 1 || len(l.fakes.Runner.Specs()) != 0 || len(l.fakes.Records.All()) != 1 {
		t.Errorf("Leave it: popped %d, %d hatchzip runs, %d records", l.popped, len(l.fakes.Runner.Specs()), len(l.fakes.Records.All()))
	}
}

func TestRestoreNeedsAYes(t *testing.T) {
	r := unfinished(newRig(t, scripted(extracttest.Script{Stdout: restoredOut})))
	r.start().send(enter(), keyCode(tea.KeyDown), enter()).wantState("restoreConfirm")
	r.wantView("Restore "+zipName+"?", "rebuilds the archive from the entries it has not extracted yet", zipOut)

	// Enter on the fresh dialog is No.
	r.send(enter()).wantState("unfinished")
	// So is a forged Yes while the dialog is not up, and another dialog's.
	r.send(
		confirm.AnsweredMsg{ID: restoreDialogID, Answer: confirm.AnswerYes},
		confirm.AnsweredMsg{ID: runDialogID, Answer: confirm.AnswerYes},
	)
	if r.ran("restore") != 0 || r.state() != "unfinished" {
		t.Fatalf("a restore ran without a Yes (state %q)", r.state())
	}

	r.send(enter()).wantState("restoreConfirm").key("y")
	r.until("restored")
	if r.ran("restore") != 1 || !r.yes[restoreDialogID] {
		t.Fatalf("%d restores, dialog yes %v", r.ran("restore"), r.yes[restoreDialogID])
	}
	if args := r.lastSpec().Args; args[0] != "restore" || !contains(args, zipPath) {
		t.Errorf("the restore argv is %v", args)
	}
	r.wantView("Restored.", "working archive again", zipOut)
	// Devpit's own record goes; the side files are hatchzip's to remove.
	if n := len(r.fakes.Records.All()); n != 0 {
		t.Errorf("%d records left after a restore", n)
	}
	if !r.fakes.FS.Exists(zipPath + ".hatchzip") {
		t.Error("the screen removed hatchzip's journal")
	}
}

func TestARestoreThatFailsSaysWhy(t *testing.T) {
	r := unfinished(newRig(t, scripted(extracttest.Script{Exit: 1, Stderr: "error: D:\\Downloads\\Game-Backup.zip is not partially extracted; there is nothing to restore\n"})))
	r.start().send(enter(), keyCode(tea.KeyDown), enter()).key("y").until("restored")
	r.wantView("hatchzip could not restore the archive:", "is not partially extracted; there is nothing to restore")
	if n := len(r.fakes.Records.All()); n != 1 {
		t.Errorf("a failed restore dropped the record (%d left)", n)
	}
	r.key("r").wantState("unfinished")
}

// TestHatchzipIsInstalledOnlyAfterAYes is rule 88 at the screen.
func TestHatchzipIsInstalledOnlyAfterAYes(t *testing.T) {
	r := newRig(t, with(WithDetect(missing)), installs(stepRun{exit: 1}))
	r.start().send(enter()).wantState("tool")
	for _, k := range []string{"y", "s", "x", " "} {
		r.key(k)
	}
	r.send(confirm.AnsweredMsg{ID: installDialogID, Answer: confirm.AnswerYes})
	if n := len(r.steps.calls()); n != 0 || r.state() != "tool" {
		t.Fatalf("%d install commands ran from the offer (state %q)", n, r.state())
	}

	r.send(enter()).wantState("toolConfirm").wantView("Install hatchzip?", "[ No ]")
	r.send(enter()).wantState("tool") // Enter on a fresh dialog is No
	if n := len(r.steps.calls()); n != 0 {
		t.Fatalf("%d install commands ran after a No", n)
	}

	r.send(enter()).key("y")
	r.until("installFailed")
	if n := len(r.steps.calls()); n != 1 || !r.yes[installDialogID] {
		t.Fatalf("%d install commands, dialog yes %v", n, r.yes[installDialogID])
	}
	// The bucket add failed, so the install itself never ran.
	r.wantView("hatchzip was not installed", "Adding the xevrion bucket", "not run")
}

func TestTheInstallScreenShowsTheExactCommands(t *testing.T) {
	const (
		bucket  = "scoop bucket add xevrion https://github.com/xevrion/scoop-bucket"
		install = "scoop install xevrion/hatchzip"
	)
	r := newRig(t, with(WithDetect(missing)), installs(stepRun{}, stepRun{}))
	r.start().send(enter()).wantState("tool")
	r.wantView("Extracting in place uses hatchzip, a free open-source tool (MIT/Apache 2.0)", "It is not installed yet.",
		"Install hatchzip with Scoop", "Devpit will run exactly:", "  "+bucket, "  "+install)
	r.send(enter()).wantState("toolConfirm").wantView("Devpit will run exactly:", bucket, install)

	// What it ran is what it showed, in that order, and nothing else.
	r.key("y").untilFunc("the steps", func(Model) bool { return len(r.steps.calls()) == 2 })
	calls := r.steps.calls()
	if got := strings.Join(calls[0], " "); got != bucket {
		t.Errorf("step 1 ran %q", got)
	}
	if got := strings.Join(calls[1], " "); got != install {
		t.Errorf("step 2 ran %q", got)
	}

	// Too old: the update commands, shown the same way.
	old := newRig(t, with(WithDetect(foundTool("0.1.8"))))
	old.start().send(enter()).wantState("tool")
	old.wantView("hatchzip 0.1.8 is installed. Devpit needs 0.1.10 or newer.", "Update hatchzip with Scoop", "  scoop update", "  scoop update hatchzip")
}

func TestAfterInstallTheScreenChecksByItself(t *testing.T) {
	var installed atomic.Bool
	detect := func(ctx context.Context) tools.Tool {
		if installed.Load() {
			return foundTool(extracttest.ToolVersion)(ctx)
		}
		return tools.Tool{}
	}
	steps := &fakeSteps{runs: []stepRun{
		{lines: []string{"The 'xevrion' bucket already exists. To add it again, first remove it by running 'scoop bucket rm xevrion'."}, exit: 1},
		{lines: []string{"'hatchzip' (0.1.10) was installed successfully!"}},
	}}
	run := func(ctx context.Context, argv []string, d time.Duration, onLine func(string)) tools.StepResult {
		res := steps.run(ctx, argv, d, onLine)
		if len(steps.calls()) == 2 {
			installed.Store(true)
		}
		return res
	}
	r := newRig(t, with(WithDetect(detect), WithRunStep(run)), scripted(planFits()))
	r.start().send(enter()).wantState("tool")
	r.send(enter()).key("y")
	// No key from here: the install ends, hatchzip is found, the check runs.
	r.until("check")
	r.wantView("Extract "+zipName, "Fits in place")
	if len(steps.calls()) != 2 {
		t.Fatalf("%d install commands ran; a bucket that already exists counts as added", len(steps.calls()))
	}
	if r.ran("check") != 1 || r.ran("extract") != 0 {
		t.Errorf("%d checks, %d extract runs", r.ran("check"), r.ran("extract"))
	}
}

func TestAnInstallThatLeavesNoHatchzipSaysSo(t *testing.T) {
	r := newRig(t, with(WithDetect(missing)), installs(stepRun{}, stepRun{}))
	r.start().send(enter(), enter()).key("y").until("tool")
	r.wantView("The install finished, but Devpit still cannot find a usable hatchzip.")
}

func TestSkippingAnInstallStepTakesTwoPresses(t *testing.T) {
	r := newRig(t, with(WithDetect(missing)), installs(stepRun{hold: true}))
	r.start().send(enter(), enter()).key("y").wantState("installing")
	if !r.m.Busy() {
		t.Fatal("an install in flight is not busy")
	}
	r.key("s").wantState("installing")
	if r.m.inst.skipped {
		t.Fatal("one press of s skipped the step")
	}
	r.key("s").until("installFailed")
	r.wantView("hatchzip was not installed", "skipped by you")
	r.key("r").wantState("tool")
}

func TestNoScoopSaysWhereToGetIt(t *testing.T) {
	r := newRig(t, with(WithDetect(missing), WithScoop(missing)), scripted(planFits()))
	r.start().send(enter()).wantState("tool")
	r.wantView("hatchzip is not installed. Devpit installs it with Scoop, which is not on", "https://scoop.sh",
		"https://github.com/xevrion/hatchzip", "press r to check again")
	for _, gone := range []string{"Install hatchzip with Scoop", "Devpit will run exactly"} {
		if strings.Contains(r.view(), gone) {
			t.Errorf("without Scoop the card still says %q:\n%s", gone, r.view())
		}
	}
	// Enter installs nothing; r looks again.
	r.send(enter()).wantState("tool")
	if len(r.steps.calls()) != 0 {
		t.Fatal("an install ran without Scoop")
	}
	r.m.detect = foundTool(extracttest.ToolVersion)
	r.key("r").until("check")
}

// TestStaleProgressMessagesAreDropped: results carry the generation they
// were started in, and one from work the screen has moved on from changes
// nothing.
func TestStaleProgressMessagesAreDropped(t *testing.T) {
	r := newRig(t, scripted(planFits(), extractHangs())).toRunning()
	r.untilFunc("hatchzip extract started", func(Model) bool { return r.ran("extract") == 1 })
	before := r.m.prog
	old := r.m.gen - 1

	r.deliver(progressMsg{gen: old, p: extractengine.Progress{DoneBytes: 95_000_000_000, TotalBytes: 96_000_000_000, Current: "stale.bin"}})
	if r.m.prog.DoneBytes != before.DoneBytes || r.m.prog.Current == "stale.bin" {
		t.Errorf("a stale progress message moved the bar: %+v", r.m.prog)
	}
	r.deliver(outcomeMsg{gen: old, out: extractengine.Outcome{Kind: extractengine.KindDone}})
	r.deliver(startedMsg{gen: old, err: context.Canceled})
	r.deliver(checkedMsg{gen: old, res: extractengine.CheckResult{Verdict: extractengine.VerdictNoSpace}})
	r.deliver(stepDoneMsg{gen: old})
	r.deliver(restoredMsg{gen: old})
	if r.state() != "running" {
		t.Fatalf("a stale result moved the screen to %q", r.state())
	}

	// A current message still lands, and never moves the bar backwards.
	r.deliver(progressMsg{gen: r.m.gen, p: extractengine.Progress{Phase: extractengine.PhaseWriting, DoneBytes: 40_000_000_000, TotalBytes: 96_000_000_000}})
	r.deliver(progressMsg{gen: r.m.gen, p: extractengine.Progress{Phase: extractengine.PhaseWriting, DoneBytes: 10_000_000_000, TotalBytes: 96_000_000_000}})
	if r.m.prog.DoneBytes != 40_000_000_000 {
		t.Errorf("DoneBytes = %d, want the bar to hold at 40 GB", r.m.prog.DoneBytes)
	}
	r.send(esc()).until("paused")
}

func TestTheBarStopsShortOfAHundredUntilHatchzipExits(t *testing.T) {
	m := running(t, func(m *Model) { m.prog.DoneBytes = m.prog.TotalBytes })
	if p := m.percent(); p != 99 {
		t.Errorf("percent = %d with every byte counted and hatchzip still running", p)
	}
	if bar := m.TerminalProgress(); bar == nil || bar.Value != 99 {
		t.Errorf("the taskbar shows %+v", bar)
	}
	if v := strip(m.View(testCtx(80, 24, "unicode"))); strings.Contains(v, "100%") {
		t.Errorf("the frame shows 100%%:\n%s", v)
	}
}

func TestBusyOnlyWhileSomethingRuns(t *testing.T) {
	r := newRig(t, scripted(planFits())).toCheck()
	if r.m.Busy() || r.m.TerminalProgress() != nil {
		t.Error("the plan card is busy")
	}
	r.send(enter())
	if r.m.Busy() {
		t.Error("the confirmation is busy")
	}
	if got := r.m.Title(); got != "Free Up Disk Space · Extract" {
		t.Errorf("Title = %q", got)
	}
}

func TestTinyTerminalGetsAResizeNotice(t *testing.T) {
	m := newRig(t).start().m
	ctx := testCtx(60, 20, "unicode")
	if v := strip(m.View(ctx)); !strings.Contains(v, "needs a bigger window") {
		t.Errorf("a 60x20 window drew:\n%s", v)
	}
}

func TestDuplicateKeyPressesAreDebounced(t *testing.T) {
	// A console that delivers Enter twice must not answer the dialog it just
	// opened. With a clock that does not move between the two, the second
	// is an echo.
	at := extracttest.Epoch()
	r := newRig(t, with(WithClock(func() time.Time { return at })), scripted(planFits()))
	r.start()
	r.send(enter()).until("check")
	at = at.Add(time.Second)
	r.send(enter(), enter())
	r.wantState("confirm")
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestPasteGoesOnlyIntoAField: a pasted path or password lands in the field
// that has the keyboard, masked where the field is; on a card it is ignored,
// so pasted text can never act as key presses.
func TestPasteGoesOnlyIntoAField(t *testing.T) {
	p := newRig(t, scripted(needsPassword(), planFits()))
	p.start().send(enter()).until("password")
	p.send(tea.PasteMsg{Content: typedPw})
	if got := p.m.pass.Value(); got != typedPw {
		t.Fatalf("the pasted password is %q in the field", got)
	}
	if strings.Contains(p.view(), typedPw) {
		t.Fatalf("a pasted password is shown:\n%s", p.view())
	}
	p.send(enter()).until("check")

	// On the plan card a paste holding "y" and a line end starts nothing.
	p.send(tea.PasteMsg{Content: "y\ny\n"})
	p.wantState("check")
	if p.ran("extract") != 0 {
		t.Fatal("a paste started the extraction")
	}

	// A pasted path is checked like a typed one.
	r := typeRow(newRig(t, scripted(planFits())))
	r.send(tea.PasteMsg{Content: zipPath}).send(enter()).until("check")
}

// TestExitOneAtCheckIsTheFailedCardWithHatchzipsLine covers what hatchzip
// 0.1.10 really does (plans/v0.5.4-spikes.md): a broken zip, and a folder
// other than the one an unfinished extraction began in, both exit 1 at the
// check. The card shows hatchzip's own line, leaves no ticket, and Devpit
// never guesses a folder from the text.
func TestExitOneAtCheckIsTheFailedCardWithHatchzipsLine(t *testing.T) {
	for name, line := range map[string]string{
		"broken zip":   "this is not a ZIP archive HatchZip can read: invalid Zip archive: Could not find EOCD",
		"other folder": `this archive is already partially extracted to D:\Elsewhere\Game-Backup. Complete or restore that extraction first`,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, scripted(extracttest.Check(1, "", "error: "+line))).toCheck()
			r.wantView("hatchzip could not check this archive. Nothing was changed.", "hatchzip: "+line)
			if r.m.ticket != nil || r.m.req.OutDir != zipOut {
				t.Errorf("ticket %v, folder %q", r.m.ticket != nil, r.m.req.OutDir)
			}
			r.send(enter()).key("y")
			if r.state() != "check" || r.ran("extract") != 0 {
				t.Errorf("state %q, %d extract runs", r.state(), r.ran("extract"))
			}
		})
	}
}

// TestAnUnknownSpeedShowsNoTimeLeft: an ETA of zero means the engine does
// not know the speed yet, so nothing is shown rather than "0s left".
func TestAnUnknownSpeedShowsNoTimeLeft(t *testing.T) {
	m := running(t, func(m *Model) { m.prog.ETA, m.prog.Speed = 0, 0 })
	if v := strip(m.View(testCtx(80, 24, "unicode"))); strings.Contains(v, "left") || strings.Contains(v, "/s") {
		t.Errorf("an unknown speed is shown:\n%s", v)
	}
}

// earlyFull is the nth hatchzip run that stops on its false "disk full"
// (bug #79) after writing one more file and cutting the archive down.
func earlyFull(n int) extracttest.Script {
	return extracttest.Script{
		Exit:   1,
		Stderr: `error: not enough disk space to continue: 'textures\level07.pak' requires 941.7 MB and 169.7 MB is available (2.0 GB reserved).`,
		Steps: []extracttest.Step{{Do: func(fs *extracttest.FakeFS) {
			fs.AddFile(zipPath+archive.JournalSuffix, 4096)
			fs.SetSize(zipOut+`\part`+strconv.Itoa(n)+`.bin`, 1_000_000_000)
			fs.SetSize(zipPath, zipBytes-int64(n)*1_000_000_000)
		}}},
	}
}

// TestTheScreenCountsTheAutomaticResumes is rule 85 on screen: while hatchzip
// keeps stopping on its false "disk full" and each run gets further, the
// running screen says how many times Devpit resumed it, the bar never goes
// backwards, and the card at the end says the same count.
func TestTheScreenCountsTheAutomaticResumes(t *testing.T) {
	stuck := earlyFull(3)
	stuck.Steps = nil // the third resume gets nowhere: the disk really is full
	r := newRig(t, scripted(planFits(), earlyFull(1), earlyFull(2), earlyFull(3), stuck)).toRunning()
	var (
		last  int64
		shown = map[int]bool{}
	)
	r.untilFunc("the end", func(m Model) bool {
		if m.state != stateRunning {
			return true
		}
		if m.prog.DoneBytes < last {
			t.Errorf("the bar went backwards across a resume: %d after %d", m.prog.DoneBytes, last)
		}
		last = m.prog.DoneBytes
		if n := m.autoResumes; n > 0 && !shown[n] {
			shown[n] = true
			want := "hatchzip reported the disk full too early (a known hatchzip issue). Devpit resumed it " + times(n) + "."
			if view := strings.Join(strings.Fields(strip(m.View(r.ctx))), " "); !strings.Contains(view, want) {
				t.Errorf("the running screen does not say %q:\n%s", want, view)
			}
		}
		return false
	})
	if !shown[1] || !shown[3] {
		t.Errorf("the running screen showed the counts %v, want 1 to 3", shown)
	}
	if r.ran("extract") != 4 {
		t.Errorf("%d extract runs, want the first and 3 resumes", r.ran("extract"))
	}
	r.wantState("failed")
	view := strings.Join(strings.Fields(r.view()), " ")
	for _, want := range []string{
		"hatchzip stopped: not enough disk space to continue",
		"hatchzip reported the disk full too early 3 times, and Devpit already resumed it by itself each time.",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the failed card does not say %q:\n%s", want, r.view())
		}
	}
}

// TestATypedPathWithNoFileSaysSo: a path with nothing at it is not called a
// folder; a folder still is.
func TestATypedPathWithNoFileSaysSo(t *testing.T) {
	typeRow(newRig(t)).typed(`D:\Downloads\missing.zip`).send(enter()).
		wantState("pick").wantView("There is no file at that path.")

	r := newRig(t)
	r.fakes.FS.AddDir(`D:\Downloads\folder.zip`)
	typeRow(r).typed(`D:\Downloads\folder.zip`).send(enter()).
		wantState("pick").wantView("That is a folder, not an archive file.")
}
