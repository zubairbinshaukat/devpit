package share

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/share/errmap"
	"github.com/zubairbinshaukat/devpit/internal/share/host"
	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/share/robocopy"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// frameWithKeys is the body of a screen followed by its footer key hints, so a
// golden also pins what the footer would offer in that state.
func frameWithKeys(scr uictx.Screen, ctx uictx.Context) string {
	var hints []string
	for _, b := range scr.ShortHelp() {
		h := b.Help()
		hints = append(hints, h.Key+" "+h.Desc)
	}
	return platformNeutral(strip(scr.View(ctx))) + "\n\n[keys] " + strings.Join(hints, "  ·  ") + "\n"
}

// platformNeutral makes a frame the same on every OS. The only thing in these
// screens that depends on the build is the folder picker's Browse row, whose
// description comes from pathpicker's per-OS constant: off Windows the row is
// disabled and says so. The share package cannot set that row, so the frame
// is pinned in its Windows form, the one Devpit ships. (A pathpicker option to
// say whether the browser is available would make this unnecessary.)
func platformNeutral(frame string) string {
	return strings.ReplaceAll(frame, "Only available on Windows", "Open the Windows folder browser")
}

// TestFramesDoNotDependOnTheOS guards the fix for goldens that passed on
// Linux and failed on Windows: an error's detail line printed the errno's own
// text, which is "errno 1219" on Linux and a long localised sentence on
// Windows. It must be the stable "System error N" whatever the OS.
func TestFramesDoNotDependOnTheOS(t *testing.T) {
	for _, st := range recvStates() {
		if !strings.HasPrefix(st.name, "recv_error_") {
			continue
		}
		frame := frameWithKeys(st.build(t), testCtx(80, 24, icons.TierUnicode))
		if !strings.Contains(frame, "System error ") {
			t.Errorf("%s has no stable error number:\n%s", st.name, frame)
		}
		for _, osText := range []string{"errno", "Multiple connections", "Access is denied", "host is down", "…"} {
			if strings.Contains(frame, osText) {
				t.Errorf("%s shows OS-dependent text %q:\n%s", st.name, osText, frame)
			}
		}
	}
}

func TestErrorDetailKeepsTheContextAndTheNumber(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("copying: %w", syscall.Errno(112)), "copying: System error 112"},
		{syscall.Errno(1219), "System error 1219"},
		{fmt.Errorf("signing in to \\\\h\\s: %w", syscall.Errno(1326)), `signing in to \\h\s: System error 1326`},
		{errors.New("that PC has no shared folders you can see"), "that PC has no shared folders you can see"},
	}
	for _, tt := range tests {
		if got := errorDetail(tt.err); got != tt.want {
			t.Errorf("errorDetail(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

// goldenState is one named state of a screen.
type goldenState struct {
	name  string
	build func(t *testing.T) uictx.Screen
}

// recvIn returns a receiving screen driven to a state by real messages.
func recvIn(t *testing.T, r *fakeReceiver, steps ...tea.Msg) *harness {
	t.Helper()
	return newRecvHarness(t, r).send(steps...)
}

func enter() tea.Msg { return keyCode(tea.KeyEnter) }

// asRecv returns the harness's screen as a receiving screen.
func asRecv(h *harness) recvScreen { return h.scr.(recvScreen) }

func hostStates() []goldenState {
	base := func(t *testing.T, f *fakeHoster) *harness { return newHostHarness(t, f, new([]string)) }
	return []goldenState{
		{"host_pick", func(t *testing.T) uictx.Screen { return base(t, newFakeHoster()).scr }},
		{"host_adapters", func(t *testing.T) uictx.Screen {
			f := newFakeHoster()
			f.adapters = []host.Adapter{
				{Name: "Wi-Fi", Index: 12, IP: net.ParseIP("192.168.1.20"), Best: true},
				{Name: "Ethernet", Index: 4, IP: net.ParseIP("10.0.0.5")},
			}
			return base(t, f).send(chosen(`D:\Games`)).scr
		}},
		{"host_private", func(t *testing.T) uictx.Screen {
			f := newFakeHoster()
			f.cat = host.CategoryPublic
			return base(t, f).send(chosen(`D:\Games`)).scr
		}},
		{"host_setup", func(t *testing.T) uictx.Screen {
			s := base(t, newFakeHoster()).scr.(hostScreen)
			s.stage, s.steps, s.stepAt = hostSetup, []host.Step{{Key: "firewall"}, {Key: "login"}}, 1
			return s
		}},
		{"host_setup_waiting_for_uac", func(t *testing.T) uictx.Screen {
			s := base(t, newFakeHoster()).scr.(hostScreen)
			s.stage = hostSetup
			return s
		}},
		{"host_setup_private", func(t *testing.T) uictx.Screen {
			s := base(t, newFakeHoster()).scr.(hostScreen)
			s.stage, s.private, s.steps, s.stepAt = hostSetup, true, []host.Step{{Key: "network"}, {Key: "firewall"}, {Key: "login"}, {Key: "folder"}}, 3
			return s
		}},
		{"host_card", func(t *testing.T) uictx.Screen { return base(t, newFakeHoster()).send(chosen(`D:\Games`)).scr }},
		{"host_card_two_adapters", func(t *testing.T) uictx.Screen {
			f := newFakeHoster()
			f.adapters = []host.Adapter{
				{Name: "Wi-Fi", Index: 12, IP: net.ParseIP("192.168.1.20"), Best: true},
				{Name: "Ethernet", Index: 4, IP: net.ParseIP("10.0.0.5")},
			}
			return base(t, f).send(chosen(`D:\Games`), enter()).scr
		}},
		{"host_card_network_switched", func(t *testing.T) uictx.Screen {
			f := newFakeHoster()
			f.cat = host.CategoryPublic
			return base(t, f).send(chosen(`D:\Games`), text("y")).scr
		}},
		{"host_confirm_stop", func(t *testing.T) uictx.Screen {
			return base(t, newFakeHoster()).send(chosen(`D:\Games`), text("s")).scr
		}},
		{"host_stopping", func(t *testing.T) uictx.Screen {
			s := base(t, newFakeHoster()).scr.(hostScreen)
			s.stage = hostStopping
			return s
		}},
		{"host_stopped", func(t *testing.T) uictx.Screen {
			return base(t, newFakeHoster()).send(chosen(`D:\Games`), text("s"), text("y")).scr
		}},
		{"host_failed", func(t *testing.T) uictx.Screen {
			f := newFakeHoster()
			f.startErr = errors.New("sharing the folder: The specified share already exists")
			return base(t, f).send(chosen(`D:\Games`)).scr
		}},
		{"host_declined", func(t *testing.T) uictx.Screen {
			f := newFakeHoster()
			f.startErr = &elevate.DeclinedError{}
			return base(t, f).send(chosen(`D:\Games`)).scr
		}},
		{"host_stop_failed", func(t *testing.T) uictx.Screen {
			f := newFakeHoster()
			h := base(t, f).send(chosen(`D:\Games`), text("s"))
			f.stopErr = errors.New("removing the account: Access is denied")
			return h.send(text("y")).scr
		}},
		{"host_no_network", func(t *testing.T) uictx.Screen {
			f := newFakeHoster()
			f.adapters = nil
			return base(t, f).send(chosen(`D:\Games`)).scr
		}},
	}
}

func recvStates() []goldenState {
	saved := func() job.Job {
		return job.Job{
			Host: "192.168.1.5", Share: "Games", User: "bob", Dest: `D:\Games`, TotalBytes: 80 << 30, DoneBytes: 47 << 30,
			TotalFiles: 1200, DoneFiles: 700, State: job.StatePaused, LastError: "The other PC stopped answering.",
		}
	}
	copying := func(t *testing.T, e recv.Event) uictx.Screen {
		s := asRecv(recvIn(t, newRecvFake()))
		s.stage, s.host, s.ev = recvCopy, "192.168.1.5", e
		return s
	}
	toCheck := func(t *testing.T, r *fakeReceiver) *harness {
		return recvIn(t, r).typed("192.168.1.5").send(enter(), enter(), enter(), enter(), chosen(`D:\Games`))
	}
	recent := []string{
		`\\192.168.1.5\Games\Data\level01.pak`, `\\192.168.1.5\Games\Data\level02.pak`,
		`\\192.168.1.5\Games\bin\game.exe`, `\\192.168.1.5\Games\Data\music\theme.ogg`,
	}
	return []goldenState{
		{"recv_ip", func(t *testing.T) uictx.Screen { return recvIn(t, newRecvFake()).scr }},
		{"recv_ip_typed", func(t *testing.T) uictx.Screen { return recvIn(t, newRecvFake()).typed("192.168.1.5").scr }},
		{"recv_listing", func(t *testing.T) uictx.Screen {
			s := asRecv(recvIn(t, newRecvFake()))
			s.stage = recvListing
			return s
		}},
		{"recv_creds_list", func(t *testing.T) uictx.Screen {
			r := newRecvFake()
			r.listErr = syscall.Errno(5)
			return recvIn(t, r).typed("192.168.1.5").send(enter()).scr
		}},
		{"recv_shares", func(t *testing.T) uictx.Screen {
			return recvIn(t, newRecvFake()).typed("192.168.1.5").send(enter()).scr
		}},
		{"recv_creds_share", func(t *testing.T) uictx.Screen {
			return recvIn(t, newRecvFake()).typed("192.168.1.5").send(enter(), enter()).scr
		}},
		{"recv_creds_typed", func(t *testing.T) uictx.Screen {
			return recvIn(t, newRecvFake()).typed("192.168.1.5").send(enter(), enter()).
				typed("bob").send(keyCode(tea.KeyTab)).typed("hunter2").scr
		}},
		{"recv_dest", func(t *testing.T) uictx.Screen {
			return recvIn(t, newRecvFake()).typed("192.168.1.5").send(enter(), enter(), enter(), enter()).scr
		}},
		{"recv_checking", func(t *testing.T) uictx.Screen {
			s := asRecv(recvIn(t, newRecvFake()))
			s.stage = recvChecking
			return s
		}},
		{"recv_check_ok", func(t *testing.T) uictx.Screen { return toCheck(t, newRecvFake()).scr }},
		{"recv_check_no_space", func(t *testing.T) uictx.Screen {
			r := newRecvFake()
			r.check = recv.Check{
				Plan: robocopy.Plan{Files: 1200, Bytes: 80 << 30}, FreeBytes: 12 << 30, FileSystem: "NTFS",
				Warnings: []recv.Warning{{Kind: recv.WarnNoSpace, Text: "This needs 80.0 GB. The destination has only 12.0 GB free. Free some space or choose another disk.", Blocks: true}},
			}
			return toCheck(t, r).scr
		}},
		{"recv_check_fat32", func(t *testing.T) uictx.Screen {
			r := newRecvFake()
			r.check = recv.Check{
				Plan: robocopy.Plan{Files: 1200, Bytes: 20 << 30, OverFAT32Count: 2}, FreeBytes: 60 << 30, FileSystem: "FAT32",
				Warnings: []recv.Warning{{Kind: recv.WarnFAT32, Text: "The destination is FAT32. 2 file(s) are bigger than 4 GB and cannot be stored there. Choose an NTFS or exFAT disk.", Blocks: true}},
			}
			return toCheck(t, r).scr
		}},
		{"recv_check_long_paths", func(t *testing.T) uictx.Screen {
			r := newRecvFake()
			r.check = recv.Check{
				Plan: robocopy.Plan{Files: 1200, Bytes: 20 << 30, LongPathCount: 14}, FreeBytes: 60 << 30, FileSystem: "NTFS",
				Warnings: []recv.Warning{{Kind: recv.WarnLongPaths, Text: "14 file(s) will have a path longer than 260 characters. They copy, but some programs cannot open them. A shorter destination folder helps."}},
			}
			return toCheck(t, r).scr
		}},
		{"recv_copy_start", func(t *testing.T) uictx.Screen {
			return copying(t, recv.Event{Phase: recv.PhaseCopying, TotalBytes: 80 << 30, TotalFiles: 1200, Attempt: 1, Elapsed: 2 * time.Second})
		}},
		{"recv_copy", func(t *testing.T) uictx.Screen {
			return copying(t, recv.Event{
				Phase: recv.PhaseCopying, DoneBytes: 47 << 30, TotalBytes: 80 << 30, DoneFiles: 700, TotalFiles: 1200,
				Speed: 118 * 1024 * 1024, ETA: 4*time.Minute + 46*time.Second, Recent: recent, Attempt: 1, Elapsed: 6*time.Minute + 40*time.Second,
			})
		}},
		{"recv_copy_retrying", func(t *testing.T) uictx.Screen {
			return copying(t, recv.Event{
				Phase: recv.PhaseRetrying, DoneBytes: 47 << 30, TotalBytes: 80 << 30, DoneFiles: 700, TotalFiles: 1200,
				Speed: 3 * 1024 * 1024, Recent: recent, Attempt: 1, Elapsed: 7 * time.Minute, Note: "The network hiccuped. Trying that file again.",
			})
		}},
		{"recv_copy_waiting", func(t *testing.T) uictx.Screen {
			return copying(t, recv.Event{
				Phase: recv.PhaseWaiting, DoneBytes: 47 << 30, TotalBytes: 80 << 30, DoneFiles: 700, TotalFiles: 1200,
				Recent: recent, Attempt: 2, Elapsed: 9 * time.Minute, NextTry: 8 * time.Second,
				Note: "The other PC stopped answering.",
			})
		}},
		{"recv_done", func(t *testing.T) uictx.Screen {
			s := asRecv(recvIn(t, newRecvFake()))
			s.stage, s.dest = recvDone, `D:\Games`
			s.job = job.Job{TotalBytes: 80 << 30, TotalFiles: 1200}
			s.out = recv.Outcome{Done: true, Message: "All files were copied.", Elapsed: 14*time.Minute + 2*time.Second}
			return s
		}},
		{"recv_failed", func(t *testing.T) uictx.Screen {
			s := asRecv(recvIn(t, newRecvFake()))
			s.stage = recvFailed
			s.job = job.Job{TotalBytes: 80 << 30}
			s.out = recv.Outcome{Message: "Some files could not be copied.", DoneBytes: 79 << 30, Failed: []recv.FailedFile{
				{Path: `\\192.168.1.5\Games\Data\big-video.mp4`, Code: 112, Reason: "This disk is full"},
				{Path: `\\192.168.1.5\Games\save\slot1.dat`, Code: 32, Reason: "A file is in use"},
			}}
			return s
		}},
		{"recv_resume", func(t *testing.T) uictx.Screen {
			j := saved()
			return newRecvScreen(testDeps(newFakeHoster(), newRecvFake(), new([]string)), config.Default(), &j)
		}},
		{"recv_resume_password", func(t *testing.T) uictx.Screen {
			r := newRecvFake()
			r.checkErr = syscall.Errno(1326)
			j := saved()
			h := newHarness(t, newRecvScreen(testDeps(newFakeHoster(), r, new([]string)), config.Default(), &j)).init()
			return h.send(enter()).scr
		}},
		{"recv_error_session_conflict", func(t *testing.T) uictx.Screen {
			r := newRecvFake()
			r.signShare = syscall.Errno(1219)
			return recvIn(t, r).typed("192.168.1.5").send(enter(), enter()).typed("bob").send(keyCode(tea.KeyTab)).typed("pw").send(enter()).scr
		}},
		{"recv_error_microsoft_account", func(t *testing.T) uictx.Screen {
			r := newRecvFake()
			r.signShare = syscall.Errno(1326)
			return recvIn(t, r).typed("192.168.1.5").send(enter(), enter()).typed("me@example.com").send(keyCode(tea.KeyTab)).typed("pw").send(enter()).scr
		}},
		{"recv_error_wrong_password", func(t *testing.T) uictx.Screen {
			r := newRecvFake()
			r.signShare = syscall.Errno(86)
			return recvIn(t, r).typed("192.168.1.5").send(enter(), enter()).typed("bob").send(keyCode(tea.KeyTab)).typed("pw").send(enter()).scr
		}},
		{"recv_error_not_found", func(t *testing.T) uictx.Screen {
			r := newRecvFake()
			r.listErr = syscall.Errno(53)
			return recvIn(t, r).typed("192.168.1.5").send(enter()).scr
		}},
		{"recv_error_guest_blocked", func(t *testing.T) uictx.Screen {
			r := newRecvFake()
			r.listErr = syscall.Errno(1272)
			r.signHostEr = syscall.Errno(1272)
			return recvIn(t, r).typed("192.168.1.5").send(enter(), enter()).send(enter()).scr
		}},
		{"recv_error_blank_password", func(t *testing.T) uictx.Screen {
			r := newRecvFake()
			r.signShare = syscall.Errno(1327)
			return recvIn(t, r).typed("192.168.1.5").send(enter(), enter()).typed("bob").send(keyCode(tea.KeyTab)).typed("x").send(enter()).scr
		}},
		{"recv_error_access_denied", func(t *testing.T) uictx.Screen {
			r := newRecvFake()
			r.checkErr = syscall.Errno(5)
			h := recvIn(t, r).typed("192.168.1.5").send(enter(), enter(), enter(), enter())
			// Sign-in was skipped, so a login-type error asks for it; give one.
			return h.send(chosen(`D:\Games`)).scr
		}},
		{"recv_error_copy_disk_full", func(t *testing.T) uictx.Screen {
			s := asRecv(recvIn(t, newRecvFake()))
			s.stage, s.err, s.errPhase = recvError, fmt.Errorf("copying: %w", syscall.Errno(112)), errmap.PhaseCopy
			return s
		}},
		{"recv_error_share_removed", func(t *testing.T) uictx.Screen {
			s := asRecv(recvIn(t, newRecvFake()))
			s.stage, s.err, s.errPhase = recvError, fmt.Errorf("copying: %w", syscall.Errno(67)), errmap.PhaseCopy
			return s
		}},
	}
}

func menuStates() []goldenState {
	return []goldenState{
		{"menu", func(t *testing.T) uictx.Screen { return NewWith(Deps{}) }},
		{"menu_resume_and_cleanup", func(t *testing.T) uictx.Screen {
			m := NewWith(Deps{})
			m.hasJob, m.hasLeft = true, true
			m.saved = job.Job{Host: "192.168.1.5", Share: "Games", Dest: `D:\Games`, TotalBytes: 80 << 30, DoneBytes: 47 << 30, State: job.StatePaused}
			m.left = host.Manifest{Path: `D:\Games`, User: "devpit-ab12"}
			m.menu = m.menu.SetItems(m.items())
			return m
		}},
		{"cleanup_ask", func(t *testing.T) uictx.Screen {
			return newCleanupScreen(Deps{}, host.Manifest{Path: `D:\Games`, User: "devpit-ab12"})
		}},
		{"cleanup_running", func(t *testing.T) uictx.Screen {
			s := newCleanupScreen(Deps{}, host.Manifest{Path: `D:\Games`})
			s.stage = cleanupRunning
			return s
		}},
		{"cleanup_done", func(t *testing.T) uictx.Screen {
			s := newCleanupScreen(Deps{}, host.Manifest{Path: `D:\Games`})
			s.stage = cleanupDone
			return s
		}},
		{"cleanup_declined", func(t *testing.T) uictx.Screen {
			s := newCleanupScreen(Deps{}, host.Manifest{Path: `D:\Games`})
			s.stage, s.err = cleanupFailed, &elevate.DeclinedError{}
			return s
		}},
		{"cleanup_failed", func(t *testing.T) uictx.Screen {
			s := newCleanupScreen(Deps{}, host.Manifest{Path: `D:\Games`})
			s.stage, s.err = cleanupFailed, errors.New("cleaning up the old share: removing the account: Access is denied")
			return s
		}},
	}
}

// TestGoldenFrames pins every state of every share screen at 80x24 on the
// unicode tier, the tightest size the app draws in.
func TestGoldenFrames(t *testing.T) {
	var all []goldenState
	all = append(all, menuStates()...)
	all = append(all, hostStates()...)
	all = append(all, recvStates()...)
	seen := map[string]bool{}
	for _, st := range all {
		if seen[st.name] {
			t.Fatalf("duplicate state %q", st.name)
		}
		seen[st.name] = true
		t.Run(st.name, func(t *testing.T) {
			scr := st.build(t)
			requireGolden(t, "share_"+st.name+"_80x24_unicode_nocolor", frameWithKeys(scr, testCtx(80, 24, icons.TierUnicode)))
		})
	}
}

// TestGoldenFramesAscii pins the main states on the plain tier at 100x30, so
// a terminal with no unicode still gets a card and a progress bar.
func TestGoldenFramesAscii(t *testing.T) {
	want := map[string]bool{"menu": true, "host_card": true, "recv_check_ok": true, "recv_copy": true, "recv_done": true, "recv_error_session_conflict": true}
	var all []goldenState
	all = append(all, menuStates()...)
	all = append(all, hostStates()...)
	all = append(all, recvStates()...)
	for _, st := range all {
		if !want[st.name] {
			continue
		}
		t.Run(st.name, func(t *testing.T) {
			scr := st.build(t)
			ctx := testCtx(100, 30, icons.TierASCII)
			requireGolden(t, "share_"+st.name+"_100x30_ascii_nocolor", frameWithKeys(scr, ctx))
		})
	}
}

// TestNoGoldenFrameContainsAPasswordTypedIntoAField guards the frames
// themselves: the typed password ("hunter2") must never be in any of them.
func TestNoGoldenFrameContainsATypedPassword(t *testing.T) {
	for _, st := range recvStates() {
		scr := st.build(t)
		if strings.Contains(frameWithKeys(scr, testCtx(80, 24, icons.TierUnicode)), "hunter2") {
			t.Errorf("state %q shows the typed password", st.name)
		}
	}
}
