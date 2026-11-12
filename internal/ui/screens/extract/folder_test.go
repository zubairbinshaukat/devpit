package extract

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	extractengine "github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/extract/extracttest"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
)

// offered reports whether a list of key hints holds a key.
func offered(hints []key.Binding, k string) bool {
	for _, b := range hints {
		if b.Help().Key == k {
			return true
		}
	}
	return false
}

// TestPickingAFolderChecksAgainAndStartsNothing: the picked folder is the
// parent of a new one, the new one gets a check of its own, and that is where
// it stops. The run is still behind the confirmation.
func TestPickingAFolderChecksAgainAndStartsNothing(t *testing.T) {
	r := newRig(t, scripted(planFits(), planOther(), planOtherKeep(), planOther())).toCheck()
	if !offered(r.m.ShortHelp(), keyFolder) || !offered(r.m.FullHelp()[0], keyFolder) {
		t.Fatalf("the plan card does not offer %q: %v", keyFolder, r.m.ShortHelp())
	}
	first := r.m.ticket
	r.fakes.FS.AddDir(otherParent)

	r.key("o").wantState("folder").wantView("Where should "+zipName+" be extracted?", "a new folder for it inside the one you pick")
	if r.ran("check") != 1 || r.m.req.OutDir != zipOut {
		t.Fatalf("opening the picker changed something: %d checks, out %q", r.ran("check"), r.m.req.OutDir)
	}

	r.typeFolder(otherParent).until("check")
	if r.m.req.OutDir != otherOut {
		t.Fatalf("the output folder is %q, want the new folder inside the picked one, %q", r.m.req.OutDir, otherOut)
	}
	if r.ran("check") < 2 || !contains(r.lastSpec().Args, otherOut) {
		t.Fatalf("the new folder was not checked: %d checks, last %v", r.ran("check"), r.lastSpec().Args)
	}
	if r.ran("extract") != 0 || r.state() != "check" {
		t.Fatalf("picking a folder started something: state %q, %d extract runs", r.state(), r.ran("extract"))
	}
	if r.m.ticket == nil || r.m.ticket == first {
		t.Error("the new folder has no ticket of its own")
	}
	// Another drive has room for the archive too, so keeping it is the
	// default again; the card says what in place would and would not do.
	r.wantView("in "+otherOut, "Free on E:", "That folder is on another drive.", "need their full size free on E:")
	if r.m.req.Mode != extractengine.ModeKeepArchive {
		t.Errorf("with room on the other drive, the mode is %v", r.m.req.Mode)
	}
	r.key("i").until("check").wantView("Fits in place", "! That folder is on another drive.", "hatchzip: the destination is on another drive")

	// The run is where it always was: behind the dialog, default No.
	r.send(enter()).wantState("confirm").wantView("into " + otherOut)
	r.send(enter()).wantState("check")
	if r.ran("extract") != 0 {
		t.Errorf("%d extract runs without a Yes", r.ran("extract"))
	}
}

// TestARefusedFolderKeepsTheOldOne: a folder Devpit will not extract into is
// refused in one sentence under the picker, and the card behind it is as it
// was, ticket included.
func TestARefusedFolderKeepsTheOldOne(t *testing.T) {
	r := newRig(t, scripted(planFits())).toFolder()
	ticket := r.m.ticket

	r.typeFolder(sysParent).wantState("folder")
	r.wantView("✗ Devpit does not extract into that place (system or sign-in files).")
	if r.m.req.OutDir != zipOut || r.ran("check") != 1 || r.m.ticket != ticket {
		t.Fatalf("a refused folder changed the request: out %q, %d checks", r.m.req.OutDir, r.ran("check"))
	}

	// The next key clears the sentence; a folder that is not there is the
	// picker's own refusal, in the same place.
	r.key("z")
	if strings.Contains(r.view(), "does not extract into that place") {
		t.Errorf("the refusal outlived the next key:\n%s", r.view())
	}
	r.typeFolder(`E:\Nowhere`).wantState("folder").wantView("✗ there is no folder at that path")

	// Esc leaves the typed path, then the picker, for the card as it was.
	r.send(esc(), esc()).wantState("check").wantView("in " + zipOut)
	if r.m.ticket != ticket {
		t.Error("backing out of the picker lost the passed check")
	}
	r.send(enter()).wantState("confirm").wantView("into " + zipOut)
}

// TestANetworkFolderIsRefused covers the refusal that only the engine can
// make: a mapped network drive looks like any other letter to the picker.
func TestANetworkFolderIsRefused(t *testing.T) {
	r := newRig(t, scripted(planFits()), with(WithOutDirIn(func(string, archive.Set) (string, error) {
		return "", extractengine.ErrNetworkPath
	}))).toFolder()
	r.typeFolder(otherParent).wantState("folder")
	r.wantView("✗ Network folders are not extracted into. Pick a folder on this PC.")
	if r.m.req.OutDir != zipOut {
		t.Errorf("a refused folder became the output: %q", r.m.req.OutDir)
	}
}

// TestResumeOffersNoChangeFolder: an unfinished extraction carries on in the
// folder it was started with, so its card has no change-folder key and the
// key does nothing.
func TestResumeOffersNoChangeFolder(t *testing.T) {
	r := unfinished(newRig(t, scripted(planResuming())))
	r.start().send(enter()).wantState("unfinished").send(enter()).until("check")
	if offered(r.m.ShortHelp(), keyFolder) || offered(r.m.FullHelp()[0], keyFolder) {
		t.Errorf("a resume offers %q: %v", keyFolder, r.m.ShortHelp())
	}
	r.key("o").wantState("check")
	if r.m.req.OutDir != zipOut || r.ran("check") != 1 {
		t.Errorf("o on a resume changed something: out %q, %d checks", r.m.req.OutDir, r.ran("check"))
	}

	// Nor does a card that did not pass for a reason a folder cannot fix.
	bad := newRig(t, scripted(needsPassword())).start().send(enter()).until("password")
	bad.key("o").wantState("password")
}

// TestTheFolderDialogStartsBesideTheArchive: Browse opens the Windows dialog
// on the archive's own folder, and what it returns is picked like a typed one.
func TestTheFolderDialogStartsBesideTheArchive(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the folder dialog is a Windows one")
	}
	var (
		started string
		disk    *extracttest.FakeFS
	)
	r := newRig(t, scripted(planFits(), planOther(), planOtherKeep()), with(WithFolderPicker(
		func(p string) (os.FileInfo, error) { return disk.Lstat(p) },
		func(start string) (string, error) {
			started = start
			return otherParent, nil
		},
	)))
	disk = r.fakes.FS
	disk.AddDir(otherParent)
	r.toCheck().key("o").wantState("folder")
	r.send(keyCode(tea.KeyDown), keyCode(tea.KeyDown), enter()).until("check")
	if started != `D:\Downloads` {
		t.Errorf("the dialog started in %q, want the archive's folder", started)
	}
	if r.m.req.OutDir != otherOut {
		t.Errorf("the browsed folder gave %q", r.m.req.OutDir)
	}
}

// TestNoSpaceOffersAnotherFolder: a check that found too little room says a
// folder on another drive is a way out, and offers the key.
func TestNoSpaceOffersAnotherFolder(t *testing.T) {
	r := newRig(t, scripted(planNoFit())).toCheck()
	r.wantView("Or press o to extract into a folder on a drive with more room.")
	if !offered(r.m.ShortHelp(), keyFolder) {
		t.Errorf("the no-space card does not offer %q", keyFolder)
	}
	r.key("o").wantState("folder")
}

// TestTheLogPathIsShownAfterARun: every ending says where the run's log is,
// and so does the details view. A run that left no log says nothing.
func TestTheLogPathIsShownAfterARun(t *testing.T) {
	ends := map[string]Model{
		"done":    ended(t, stateDone, extractengine.Outcome{Kind: extractengine.KindDone, Elapsed: time.Minute, LogPath: logFile}, nil),
		"paused":  ended(t, statePaused, extractengine.Outcome{Kind: extractengine.KindPaused, Exit: 130, CanResume: true, LogPath: logFile}, nil),
		"stopped": ended(t, statePaused, extractengine.Outcome{Kind: extractengine.KindStopped, Exit: 1, CanResume: true, LogPath: logFile}, nil),
		"failed":  ended(t, stateFailed, extractengine.Outcome{Kind: extractengine.KindFailed, Exit: 1, Error: "could not write a file", LogPath: logFile}, nil),
		"details": ended(t, stateFailed, extractengine.Outcome{Kind: extractengine.KindFailed, Exit: 1, LogPath: logFile}, func(m *Model) { m.details = true }),
	}
	for name, m := range ends {
		for _, ctx := range []struct{ w, h int }{{80, 24}, {100, 30}} {
			frame := flat(strip(m.View(testCtx(ctx.w, ctx.h, icons.TierUnicode))))
			if !strings.Contains(frame, `Log: C:\Users\test\AppData\`) || !strings.Contains(frame, "extract-20260104-030405-Game-Backup.log") {
				t.Errorf("%s at %dx%d does not say where the log is:\n%s", name, ctx.w, ctx.h, strip(m.View(testCtx(ctx.w, ctx.h, icons.TierUnicode))))
			}
		}
	}

	none := ended(t, stateFailed, extractengine.Outcome{Kind: extractengine.KindFailed, Exit: 1, Error: "could not write a file"}, nil)
	if strings.Contains(strip(none.View(testCtx(80, 24, icons.TierUnicode))), "Log:") {
		t.Error("a run with no log shows a log line")
	}

	// The check's own log is on the plan card's details.
	check := newRig(t, scripted(planFits())).toCheck()
	check.m.check.LogPath = logFile
	if strings.Contains(check.view(), "Log:") {
		t.Errorf("the plan card itself shows the log:\n%s", check.view())
	}
	check.key("l").wantView("Log:", "extract-20260104-030405-Game-Backup.log")
}
