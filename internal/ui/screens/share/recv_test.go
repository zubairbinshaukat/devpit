package share

import (
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/share/robocopy"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// okCheck is a dry run that says: 3 GB, room to spare.
func okCheck() recv.Check {
	return recv.Check{
		Plan:      robocopy.Plan{Files: 120, Bytes: 3 << 30, SummaryFound: true},
		FreeBytes: 500 << 30, FileSystem: "NTFS",
	}
}

func newRecvFake() *fakeReceiver {
	return &fakeReceiver{
		listHost: "192.168.1.5",
		shares:   []netstat.Share{{Name: "Games", Type: "Disk", Comment: "My games"}, {Name: "Movies", Type: "Disk"}},
		check:    okCheck(),
		outcome:  recv.Outcome{Done: true, Message: "All files were copied.", Elapsed: 14 * time.Minute},
	}
}

func newRecvHarness(t *testing.T, r *fakeReceiver) *harness {
	t.Helper()
	scr := newRecvScreen(testDeps(newFakeHoster(), r, new([]string)), config.Default(), nil)
	return newHarness(t, scr).init()
}

// toShares types an address and lists the shares.
func toShares(h *harness) {
	h.typed("192.168.1.5").send(keyCode(tea.KeyEnter))
}

func TestReceiveHappyPathWithNoLoginNeeded(t *testing.T) {
	r := newRecvFake()
	r.events = []recv.Event{
		{Phase: recv.PhaseCopying, DoneBytes: 1 << 30, TotalBytes: 3 << 30, DoneFiles: 40, TotalFiles: 120, Speed: 110e6, ETA: 3 * time.Minute, Attempt: 1},
	}
	h := newRecvHarness(t, r)
	if !strings.Contains(h.view(), "IP address") {
		t.Fatalf("first screen:\n%s", h.view())
	}
	toShares(h)
	if h.scr.(recvScreen).stage != recvShares {
		t.Fatalf("stage %d\n%s", h.scr.(recvScreen).stage, h.view())
	}
	if v := h.view(); !strings.Contains(v, "Games") || !strings.Contains(v, "Movies") || !strings.Contains(v, "192.168.1.5") {
		t.Errorf("share list:\n%s", v)
	}
	h.send(keyCode(tea.KeyEnter)) // Games
	if h.scr.(recvScreen).stage != recvCreds {
		t.Fatalf("stage %d, want the sign-in form", h.scr.(recvScreen).stage)
	}
	if v := h.view(); !strings.Contains(v, "MicrosoftAccount") || !strings.Contains(v, "not a PIN") {
		t.Errorf("the form does not explain Microsoft accounts:\n%s", v)
	}
	// Leave both empty: no sign-in is attempted, the other PC needs none.
	h.send(keyCode(tea.KeyEnter), keyCode(tea.KeyEnter))
	if r.called("signin-share") != 0 {
		t.Error("signed in with an empty user and password")
	}
	if h.scr.(recvScreen).stage != recvDest {
		t.Fatalf("stage %d, want the destination picker", h.scr.(recvScreen).stage)
	}
	if v := h.view(); !strings.Contains(v, "Where should the files go?") {
		t.Errorf("picker:\n%s", v)
	}
	h.send(chosen(`D:\Games`))
	if h.scr.(recvScreen).stage != recvCheck {
		t.Fatalf("stage %d\n%s", h.scr.(recvScreen).stage, h.view())
	}
	v := h.view()
	for _, want := range []string{"3.0 GB", "120 files", "500 GB", "NTFS", "There is room"} {
		if !strings.Contains(v, want) {
			t.Errorf("check view lacks %q:\n%s", want, v)
		}
	}
	h.send(keyCode(tea.KeyEnter))
	rs := h.scr.(recvScreen)
	if rs.stage != recvDone {
		t.Fatalf("stage %d after the copy\n%s", rs.stage, h.view())
	}
	if r.called("run") != 1 || r.cleared != 1 || len(r.disconnect) != 1 {
		t.Errorf("run %d, cleared %d, disconnects %v", r.called("run"), r.cleared, r.disconnect)
	}
	if v := h.view(); !strings.Contains(v, "Done") || !strings.Contains(v, `D:\Games`) {
		t.Errorf("done view:\n%s", v)
	}
	h.send(keyCode(tea.KeyEnter))
	if h.popped != 1 {
		t.Error("Enter did not go back")
	}
}

// TestAFailedCopyKeepsTheSignInSoTryAgainWorks pins a bug: the screen closed
// the connection to the share after every run, so "r" on the failed-files
// screen ran robocopy with no sign-in, and on a real PC every file then
// failed with an access error. Only a finished copy disconnects.
func TestAFailedCopyKeepsTheSignInSoTryAgainWorks(t *testing.T) {
	r := newRecvFake()
	r.outcome = recv.Outcome{Message: "Some files could not be copied.", Failed: []recv.FailedFile{{Path: `\\192.168.1.5\Games\a`, Code: 32}}}
	h := newRecvHarness(t, r)
	toShares(h)
	h.send(keyCode(tea.KeyEnter))
	h.typed("bob").send(keyCode(tea.KeyTab)).typed("pw").send(keyCode(tea.KeyEnter))
	h.send(chosen(`D:\Games`), keyCode(tea.KeyEnter))
	if h.scr.(recvScreen).stage != recvFailed {
		t.Fatalf("stage %d\n%s", h.scr.(recvScreen).stage, h.view())
	}
	if len(r.disconnect) != 0 {
		t.Fatalf("a failed copy closed the connection: %v", r.disconnect)
	}
	r.outcome = recv.Outcome{Done: true, Message: "All files were copied."}
	h.send(text("r"))
	if r.called("run") != 2 || h.scr.(recvScreen).stage != recvDone {
		t.Fatalf("runs %d, stage %d", r.called("run"), h.scr.(recvScreen).stage)
	}
	if len(r.disconnect) != 1 {
		t.Errorf("disconnects after the finished retry = %v, want one", r.disconnect)
	}
}

func TestAPCThatNeedsALoginToListAsksOnceAndReusesIt(t *testing.T) {
	r := newRecvFake()
	r.listErr = syscall.Errno(5)
	r.signHost = r.shares
	h := newRecvHarness(t, r)
	toShares(h)
	if h.scr.(recvScreen).stage != recvCreds {
		t.Fatalf("stage %d, want the sign-in form after access denied", h.scr.(recvScreen).stage)
	}
	h.typed("bob").send(keyCode(tea.KeyTab)).typed("s3cret").send(keyCode(tea.KeyEnter))
	if h.scr.(recvScreen).stage != recvShares {
		t.Fatalf("stage %d\n%s", h.scr.(recvScreen).stage, h.view())
	}
	if len(r.signedIn) != 1 || r.signedIn[0].User != "bob" || r.signedIn[0].Password != "s3cret" {
		t.Errorf("signed in with %+v", r.signedIn)
	}
	// Picking a share does not ask again.
	h.send(keyCode(tea.KeyEnter))
	if h.scr.(recvScreen).stage != recvDest {
		t.Errorf("stage %d, want the destination: the sign-in is already done", h.scr.(recvScreen).stage)
	}
	if strings.Contains(h.view(), "s3cret") {
		t.Error("the password is on screen")
	}
}

func TestThePasswordFieldIsMaskedAndEmptyAfterSubmit(t *testing.T) {
	r := newRecvFake()
	h := newRecvHarness(t, r)
	toShares(h)
	h.send(keyCode(tea.KeyEnter))
	h.typed("bob").send(keyCode(tea.KeyTab)).typed("hunter2")
	if v := h.view(); strings.Contains(v, "hunter2") {
		t.Errorf("the password is shown as typed:\n%s", v)
	}
	h.send(keyCode(tea.KeyEnter))
	rs := h.scr.(recvScreen)
	if rs.pass.Value() != "" {
		t.Error("the password field still holds the password after submitting")
	}
	if r.signedIn[0].Password != "hunter2" {
		t.Errorf("signed in with %+v", r.signedIn)
	}
}

func TestAWrongPasswordForAnEmailNamesTheMicrosoftAccountFix(t *testing.T) {
	r := newRecvFake()
	r.signShare = syscall.Errno(1326)
	h := newRecvHarness(t, r)
	toShares(h)
	h.send(keyCode(tea.KeyEnter))
	h.typed("me@example.com").send(keyCode(tea.KeyTab)).typed("pw").send(keyCode(tea.KeyEnter))
	rs := h.scr.(recvScreen)
	if rs.stage != recvError {
		t.Fatalf("stage %d\n%s", rs.stage, h.view())
	}
	v := h.view()
	if !strings.Contains(v, "Microsoft account") || !strings.Contains(v, `MicrosoftAccount\`) || !strings.Contains(v, "PIN") {
		t.Errorf("error view:\n%s", v)
	}
	// Try again returns to the form with the name kept.
	h.send(text("r"))
	rs = h.scr.(recvScreen)
	if rs.stage != recvCreds || rs.user.Value() != "me@example.com" {
		t.Errorf("stage %d, user %q", rs.stage, rs.user.Value())
	}
}

func TestSessionConflictOffersToCloseOldConnectionsAndTriesAgain(t *testing.T) {
	r := newRecvFake()
	r.signShare = syscall.Errno(1219)
	r.dropN = 2
	h := newRecvHarness(t, r)
	toShares(h)
	h.send(keyCode(tea.KeyEnter))
	h.typed("bob").send(keyCode(tea.KeyTab)).typed("pw").send(keyCode(tea.KeyEnter))
	v := h.view()
	if !strings.Contains(v, "Already signed in") || !strings.Contains(v, "close the old connections") {
		t.Fatalf("view:\n%s", v)
	}
	hints := ""
	for _, b := range h.scr.ShortHelp() {
		hints += b.Help().Desc + ";"
	}
	if !strings.Contains(hints, "close old connections") {
		t.Errorf("no button for it: %q", hints)
	}
	r.signShare = nil
	h.send(text("d"))
	if r.called("drop") != 1 {
		t.Error("the old connections were not closed")
	}
	if h.scr.(recvScreen).stage != recvCreds {
		t.Errorf("stage %d, want the form again", h.scr.(recvScreen).stage)
	}
}

func TestTheOtherPCNotFoundExplainsAndRetries(t *testing.T) {
	r := newRecvFake()
	r.listErr = syscall.Errno(53)
	h := newRecvHarness(t, r)
	toShares(h)
	v := h.view()
	if !strings.Contains(v, "Cannot find that PC") || !strings.Contains(v, "same Wi-Fi") {
		t.Errorf("view:\n%s", v)
	}
	r.listErr = nil
	h.send(text("r"))
	if h.scr.(recvScreen).stage != recvShares {
		t.Errorf("stage %d after retry", h.scr.(recvScreen).stage)
	}
}

func TestABadAddressIsAnErrorScreen(t *testing.T) {
	r := newRecvFake()
	r.listErr = netstat.ErrBadHost
	h := newRecvHarness(t, r)
	h.typed("not an ip").send(keyCode(tea.KeyEnter))
	if v := h.view(); !strings.Contains(v, "Something went wrong") || !strings.Contains(v, "IP address") {
		t.Errorf("view:\n%s", v)
	}
	h.send(keyCode(tea.KeyEnter))
	if h.scr.(recvScreen).stage != recvIP {
		t.Error("Enter did not go back to the address")
	}
}

func TestNotEnoughRoomBlocksTheStart(t *testing.T) {
	r := newRecvFake()
	r.check = recv.Check{
		Plan: robocopy.Plan{Files: 5, Bytes: 80 << 30}, FreeBytes: 10 << 30,
		Warnings: []recv.Warning{{Kind: recv.WarnNoSpace, Text: "This needs 80.0 GB. The destination has only 10.0 GB free.", Blocks: true}},
	}
	h := newRecvHarness(t, r)
	toShares(h)
	h.send(keyCode(tea.KeyEnter), keyCode(tea.KeyEnter), keyCode(tea.KeyEnter), chosen(`D:\Small`))
	if v := h.view(); !strings.Contains(v, "only 10.0 GB free") || strings.Contains(v, "Press Enter to start") {
		t.Errorf("view:\n%s", v)
	}
	h.send(keyCode(tea.KeyEnter))
	if h.scr.(recvScreen).stage != recvCheck || r.called("run") != 0 {
		t.Error("the copy started although there is no room")
	}
}

func TestFailedFilesAreListedAndRetryStartsAgain(t *testing.T) {
	r := newRecvFake()
	r.outcome = recv.Outcome{
		Message:   "Some files could not be copied.",
		DoneBytes: 2 << 30,
		Failed: []recv.FailedFile{
			{Path: `\\192.168.1.5\Games\big.bin`, Code: 112, Reason: "This disk is full"},
			{Path: `\\192.168.1.5\Games\locked.dat`, Code: 32, Reason: "A file is in use"},
		},
	}
	h := newRecvHarness(t, r)
	toShares(h)
	h.send(keyCode(tea.KeyEnter), keyCode(tea.KeyEnter), keyCode(tea.KeyEnter), chosen(`D:\Games`), keyCode(tea.KeyEnter))
	if h.scr.(recvScreen).stage != recvFailed {
		t.Fatalf("stage %d\n%s", h.scr.(recvScreen).stage, h.view())
	}
	v := h.view()
	for _, want := range []string{"2 files did not copy", "big.bin", "This disk is full", "locked.dat", "Free some space"} {
		if !strings.Contains(v, want) {
			t.Errorf("failed view lacks %q:\n%s", want, v)
		}
	}
	r.outcome = recv.Outcome{Done: true}
	h.send(text("r"))
	if h.scr.(recvScreen).stage != recvDone || r.called("run") != 2 {
		t.Errorf("stage %d, runs %d after retry", h.scr.(recvScreen).stage, r.called("run"))
	}
}

func TestEscDuringACopyStopsItAndGoesBack(t *testing.T) {
	r := newRecvFake()
	r.holdRun = make(chan struct{})
	r.events = []recv.Event{{Phase: recv.PhaseCopying, DoneBytes: 100, TotalBytes: 1000, Attempt: 1}}
	h := newRecvHarness(t, r)
	toShares(h)
	h.send(keyCode(tea.KeyEnter), keyCode(tea.KeyEnter), keyCode(tea.KeyEnter), chosen(`D:\Games`))
	// Start the copy by hand: it blocks until stopped, so settle would too.
	next, cmd := h.scr.Update(keyCode(tea.KeyEnter), h.ctx)
	h.scr = next
	if !uictx.Busy(h.scr) {
		t.Fatal("a running copy is not busy")
	}
	if pr, ok := h.scr.(uictx.ProgressReporter); !ok || pr.TerminalProgress() != nil && pr.TerminalProgress().Value != 0 {
		t.Error("terminal progress is missing or wrong before the first event")
	}
	msg := runWithTimeout(t, cmd) // the first progress event
	next, cmd = h.scr.Update(msg, h.ctx)
	h.scr = next
	if got := h.scr.(recvScreen).ev.DoneBytes; got != 100 {
		t.Fatalf("the screen has %d bytes done, want 100 from the event", got)
	}
	if bar := h.scr.(uictx.ProgressReporter).TerminalProgress(); bar == nil || bar.Value != 10 {
		t.Errorf("terminal progress = %+v, want 10 percent", bar)
	}
	uictx.Stop(h.scr) // Esc or Ctrl+C
	h.settle(cmd)
	if h.popped != 1 {
		t.Errorf("popped %d, want the screen to go back after a stop", h.popped)
	}
	if r.called("run") != 1 {
		t.Errorf("runs = %d", r.called("run"))
	}
}

func TestResumeAsksThenChecksThenCopiesWithoutAskingForAPassword(t *testing.T) {
	r := newRecvFake()
	saved := job.Job{
		Host: "192.168.1.5", Share: "Games", User: "bob", Dest: `D:\Games`, TotalBytes: 80 << 30, DoneBytes: 47 << 30,
		TotalFiles: 100, DoneFiles: 60, State: job.StatePaused, LastError: "The other PC stopped answering.",
	}
	scr := newRecvScreen(testDeps(newFakeHoster(), r, new([]string)), config.Default(), &saved)
	h := newHarness(t, scr).init()
	v := h.view()
	for _, want := range []string{"A copy was interrupted", `\\192.168.1.5\Games`, "47.0 GB of 80.0 GB", "Finished files are skipped", "stopped answering"} {
		if !strings.Contains(v, want) {
			t.Errorf("resume view lacks %q:\n%s", want, v)
		}
	}
	h.send(keyCode(tea.KeyEnter))
	if r.called("run") != 1 || h.scr.(recvScreen).stage != recvDone {
		t.Errorf("stage %d, runs %d", h.scr.(recvScreen).stage, r.called("run"))
	}
	if r.signedIn != nil {
		t.Error("asked for a sign-in although Windows still had one")
	}
}

func TestResumeAsksForThePasswordWhenWindowsForgotTheSignIn(t *testing.T) {
	r := newRecvFake()
	r.checkErr = syscall.Errno(1326)
	saved := job.Job{Host: "192.168.1.5", Share: "Games", User: "bob", Dest: `D:\Games`, TotalBytes: 100, State: job.StateRunning}
	scr := newRecvScreen(testDeps(newFakeHoster(), r, new([]string)), config.Default(), &saved)
	h := newHarness(t, scr).init()
	h.send(keyCode(tea.KeyEnter))
	rs := h.scr.(recvScreen)
	if rs.stage != recvCreds || rs.credsFor != credsForResume || rs.user.Value() != "bob" {
		t.Fatalf("stage %d for %d user %q", rs.stage, rs.credsFor, rs.user.Value())
	}
	if v := h.view(); !strings.Contains(v, "forgotten the sign-in") {
		t.Errorf("view:\n%s", v)
	}
	h.typed("pw").send(keyCode(tea.KeyEnter))
	if h.scr.(recvScreen).stage != recvDone || r.signedIn[0].Password != "pw" {
		t.Errorf("stage %d, signed in %+v", h.scr.(recvScreen).stage, r.signedIn)
	}
}

func TestDiscardingASavedCopyForgetsIt(t *testing.T) {
	r := newRecvFake()
	saved := job.Job{Host: "h", Share: "s", Dest: "d", State: job.StatePaused}
	h := newHarness(t, newRecvScreen(testDeps(newFakeHoster(), r, new([]string)), config.Default(), &saved)).init()
	h.send(text("d"))
	if r.cleared != 1 || h.popped != 1 {
		t.Errorf("cleared %d, popped %d", r.cleared, h.popped)
	}
}

func TestSpeedAndTimeTextsAreEasyToRead(t *testing.T) {
	if speedText(0) != "" || speedText(118*1024*1024) != "118 MB/s" {
		t.Errorf("speed = %q / %q", speedText(0), speedText(118*1024*1024))
	}
	if etaText(0) != "" || etaText(252*time.Second) != "4m 12s left" || etaText(30*time.Second) != "30s left" {
		t.Errorf("eta = %q / %q", etaText(252*time.Second), etaText(30*time.Second))
	}
	if roundDuration(3725*time.Second) != "1h 02m" {
		t.Error(roundDuration(3725 * time.Second))
	}
	if commas(1234567) != "1,234,567" || commas(12) != "12" || commas(-1500) != "-1,500" {
		t.Errorf("commas: %s %s %s", commas(1234567), commas(12), commas(-1500))
	}
}
