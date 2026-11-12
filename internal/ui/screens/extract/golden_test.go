package extract

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	extractengine "github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/extract/extracttest"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
)

// goldenState is one named state of the screen.
type goldenState struct {
	name  string
	build func(t *testing.T) Model
}

// datasetSet is a split 7z set of eleven parts, for the picker.
func datasetSet() archive.Set {
	first := `D:\Downloads\dataset.7z.001`
	parts := make([]string, 0, 11)
	for n := 1; n <= 11; n++ {
		p, _ := archive.Volume(first, n)
		parts = append(parts, p)
	}
	return archive.Set{Picked: first, First: first, Parts: parts, Format: archive.Format7z, Bytes: 41_700_000_000}
}

// twoFound is what the picker's search finds in most frames.
func twoFound() rigOpt {
	return finds(
		extractengine.Found{Set: zipSet(), Mod: extracttest.Epoch().Add(2 * time.Hour)},
		extractengine.Found{Set: datasetSet(), Mod: extracttest.Epoch()},
	)
}

// unfinished makes the test archive an unfinished extraction: Devpit's record
// from two days ago, hatchzip's journal beside it, and the archive already
// cut down to what is left.
func unfinished(r *rig) *rig {
	r.fakes.FS.SetSize(zipPath, 34_000_000_000)
	r.fakes.FS.AddFile(zipPath+archive.JournalSuffix, 4096)
	_ = r.fakes.Records.Save([]extractengine.Record{{
		First: zipPath, OutDir: zipOut, Mode: extractengine.ModeInPlace,
		Started: extracttest.Epoch(), Tool: extracttest.ToolVersion,
	}})
	return r
}

// typeRow moves the picker to "Type a path…" and opens it.
func typeRow(r *rig) *rig {
	r.start()
	for i := 0; i < len(r.m.picker.Rows()); i++ {
		r.send(keyCode(tea.KeyDown))
	}
	r.send(enter())
	if !r.m.picker.Typing() {
		r.t.Fatalf("the picker is not typing:\n%s", r.view())
	}
	return r
}

// sampleProgress is the run of the 3.7 mock-up.
func sampleProgress() extractengine.Progress {
	return extractengine.Progress{
		Phase:     extractengine.PhaseWriting,
		DoneBytes: 56_600_000_000, TotalBytes: 96_000_000_000,
		Speed: 412_000_000, ETA: 96 * time.Second,
		DoneItems: 7310, TotalItems: 12408,
		Current:      `textures\level07.pak`,
		ArchiveBytes: 34_000_000_000, FreeBytes: 15_900_000_000,
		Elapsed: 2*time.Minute + 18*time.Second,
	}
}

// running is a screen in the middle of the sample run. The frame is set, not
// played, so the figures are the mock-up's own.
func running(t *testing.T, change func(*Model)) Model {
	t.Helper()
	m := newRig(t, scripted(planFits())).toCheck().m
	m.state = stateRunning
	m.prog = sampleProgress()
	if change != nil {
		change(&m)
	}
	return m
}

// ended is a screen showing how the sample run ended.
func ended(t *testing.T, st state, out extractengine.Outcome, change func(*Model)) Model {
	t.Helper()
	m := newRig(t, scripted(planFits())).toCheck().m
	m.state = st
	if out.Last.TotalBytes == 0 {
		out.Last = sampleProgress()
	}
	m.out = out
	if change != nil {
		change(&m)
	}
	return m
}

func goldenStates() []goldenState {
	return []goldenState{
		{"pick", func(t *testing.T) Model { return newRig(t, twoFound()).start().m }},
		{"pick_unfinished", func(t *testing.T) Model {
			return unfinished(newRig(t, finds(extractengine.Found{Set: datasetSet(), Mod: extracttest.Epoch()}))).start().m
		}},
		{"pick_typing", func(t *testing.T) Model {
			return typeRow(newRig(t, twoFound())).typed(`D:\Downloads\da`).m
		}},
		{"pick_refused_network", func(t *testing.T) Model {
			return typeRow(newRig(t, twoFound())).typed(`\\nas\backups\big.zip`).send(enter()).m
		}},
		{"pick_refused_protected", func(t *testing.T) Model {
			return typeRow(newRig(t, twoFound(), protectedPlaces())).typed(sysParent + `\Temp\big.zip`).send(enter()).m
		}},
		{"pick_refused_splitzip", func(t *testing.T) Model {
			return typeRow(newRig(t, twoFound())).typed(`D:\Downloads\big.z01`).send(enter()).m
		}},
		{"pick_refused_missing", func(t *testing.T) Model {
			return typeRow(newRig(t, twoFound())).typed(`D:\Downloads\missing.zip`).send(enter()).m
		}},

		{"tool_missing", func(t *testing.T) Model {
			return newRig(t, with(WithDetect(missing))).start().send(enter()).wantState("tool").m
		}},
		{"tool_noscoop", func(t *testing.T) Model {
			return newRig(t, with(WithDetect(missing), WithScoop(missing))).start().send(enter()).wantState("tool").m
		}},
		{"tool_old", func(t *testing.T) Model {
			return newRig(t, with(WithDetect(foundTool("0.1.8")))).start().send(enter()).wantState("tool").m
		}},
		{"install_confirm", func(t *testing.T) Model {
			return newRig(t, with(WithDetect(missing))).start().send(enter(), enter()).wantState("toolConfirm").m
		}},
		{"installing", func(t *testing.T) Model {
			r := newRig(t, with(WithDetect(missing)), installs(
				stepRun{lines: []string{"Checking repo... OK", "The xevrion bucket was added successfully."}},
				stepRun{lines: []string{"Installing 'hatchzip' (0.1.10) [64bit] from 'xevrion' bucket", "hatchzip-x86_64-pc-windows-msvc.zip (2.4 MB) [=======>      ] 58%"}, hold: true},
			))
			r.start().send(enter(), enter()).key("y").send(spinMsg{}).wantState("installing")
			m := r.m
			m.inst.rows[0].Elapsed = 2100 * time.Millisecond
			m.inst.rows[1].Elapsed = 6 * time.Second
			return m
		}},
		{"install_failed", func(t *testing.T) Model {
			r := newRig(t, with(WithDetect(missing)), installs(
				stepRun{lines: []string{"Checking repo... ERROR 'https://github.com/xevrion/scoop-bucket' doesn't look like a valid git repository", "fatal: unable to access the repository: Could not resolve host: github.com"}, exit: 1},
			))
			r.start().send(enter(), enter()).key("y").wantState("installFailed")
			m := r.m
			m.inst.rows[0].Elapsed = 3400 * time.Millisecond
			return m
		}},

		{"checking", func(t *testing.T) Model {
			m := newRig(t).start().m
			m.state, m.name = stateChecking, zipName
			return m
		}},
		{"check_fits", func(t *testing.T) Model {
			return newRig(t, scripted(planNote("the archive and the folder are on the same drive"))).toCheck().m
		}},
		{"check_keep_default", func(t *testing.T) Model {
			return newRig(t, scripted(planNote(keepNote), planKeep())).toCheck().m
		}},
		{"check_nofit", func(t *testing.T) Model { return newRig(t, scripted(planNoFit())).toCheck().m }},
		{"check_resuming", func(t *testing.T) Model {
			r := unfinished(newRig(t, scripted(planResuming())))
			r.start().send(enter()).wantState("unfinished").send(enter()).until("check")
			return r.m
		}},
		{"check_alldone", func(t *testing.T) Model {
			return newRig(t, scripted(extracttest.Check(0,
				"Game-Backup.zip  84.2 GB zip, 96.0 GB uncompressed in 12408 entries\nAll entries are already extracted\n"))).toCheck().m
		}},
		{"check_damaged", func(t *testing.T) Model {
			return newRig(t, scripted(extracttest.Check(4, "", "error: the archive is damaged: checksum mismatch in textures/level07.pak"))).toCheck().m
		}},
		{"check_badargs", func(t *testing.T) Model {
			return newRig(t, scripted(extracttest.Check(2, "", "error: unexpected argument '--keep-archive' found"))).toCheck().m
		}},

		{"folder", func(t *testing.T) Model { return newRig(t, scripted(planFits())).toFolder().m }},
		{"folder_typing", func(t *testing.T) Model {
			r := newRig(t, scripted(planFits())).toFolder()
			r.send(keyCode(tea.KeyDown), keyCode(tea.KeyDown), enter()).typed(`E:\Ga`)
			return r.m
		}},
		{"folder_refused", func(t *testing.T) Model {
			return newRig(t, scripted(planFits())).toFolder().typeFolder(sysParent).wantState("folder").m
		}},
		{"check_other_drive", func(t *testing.T) Model {
			r := newRig(t, scripted(planFits(), planOther(), planOtherKeep(), planOther())).toFolder()
			r.typeFolder(otherParent).until("check").key("i").until("check")
			return r.m
		}},
		{"check_other_drive_keep", func(t *testing.T) Model {
			r := newRig(t, scripted(planFits(), planOther(), planOtherKeep())).toFolder()
			r.typeFolder(otherParent).until("check")
			return r.m
		}},

		{"confirm_inplace", func(t *testing.T) Model { return newRig(t, scripted(planFits())).toConfirm().m }},
		{"confirm_keep", func(t *testing.T) Model {
			r := newRig(t, scripted(planFits(), planKeep())).toCheck()
			r.key("k").until("check").send(enter()).wantState("confirm")
			return r.m
		}},
		{"confirm_resume", func(t *testing.T) Model {
			r := unfinished(newRig(t, scripted(planResuming())))
			r.start().send(enter(), enter()).until("check").send(enter()).wantState("confirm")
			return r.m
		}},
		{"password", func(t *testing.T) Model {
			return newRig(t, scripted(needsPassword())).start().send(enter()).until("password").typed(typedPw).m
		}},
		{"password_wrong", func(t *testing.T) Model {
			r := newRig(t, scripted(needsPassword(), wrongPassword()))
			r.start().send(enter()).until("password").typed(typedPw).send(enter()).until("password")
			return r.m
		}},

		{"running", func(t *testing.T) Model { return running(t, nil) }},
		{"running_finishing", func(t *testing.T) Model {
			return running(t, func(m *Model) {
				m.prog.Phase, m.prog.DoneBytes, m.prog.DoneItems = extractengine.PhaseFinishing, 95_040_000_000, 12407
				m.prog.Speed, m.prog.Current, m.prog.ArchiveBytes = 0, "", 1_200_000_000
				m.prog.Elapsed = 6*time.Minute + 4*time.Second
			})
		}},
		{"running_pausing", func(t *testing.T) Model {
			return running(t, func(m *Model) { m.pausing, m.prog.Phase = true, extractengine.PhasePausing })
		}},
		{"running_stopping", func(t *testing.T) Model {
			return running(t, func(m *Model) { m.pausing, m.prog.Phase = true, extractengine.PhaseStopping })
		}},
		{"running_autoresume", func(t *testing.T) Model {
			return running(t, func(m *Model) { m.autoResumes, m.prog.AutoResumes = 3, 3 })
		}},

		{"done_inplace", func(t *testing.T) Model {
			return ended(t, stateDone, extractengine.Outcome{Kind: extractengine.KindDone, Elapsed: 6*time.Minute + 12*time.Second},
				func(m *Model) { m.freeAfter, m.freeKnown = 15_700_000_000, true })
		}},
		{"done_keep", func(t *testing.T) Model {
			return ended(t, stateDone, extractengine.Outcome{Kind: extractengine.KindDone, Elapsed: 5*time.Minute + 40*time.Second},
				func(m *Model) {
					m.req.Mode = extractengine.ModeKeepArchive
					m.freeAfter, m.freeKnown = 114_400_000_000, true
				})
		}},
		{"paused", func(t *testing.T) Model {
			return ended(t, statePaused, extractengine.Outcome{Kind: extractengine.KindPaused, Exit: 130, CanResume: true}, nil)
		}},
		{"stopped", func(t *testing.T) Model {
			return ended(t, statePaused, extractengine.Outcome{Kind: extractengine.KindStopped, Exit: 1, CanResume: true}, nil)
		}},
		{"failed", func(t *testing.T) Model {
			return ended(t, stateFailed, extractengine.Outcome{
				Kind: extractengine.KindFailed, Exit: 1, CanResume: true,
				Error: `could not write textures\level07.pak: the file is in use by another program`,
			}, nil)
		}},
		{"failed_solid", func(t *testing.T) Model {
			return ended(t, stateFailed, extractengine.Outcome{
				Kind: extractengine.KindSolid, Exit: 1,
				Error: "this RAR archive is solid: solid archives cannot be extracted in place. Run the command again with --keep-archive to extract it.",
				Last:  extractengine.Progress{TotalBytes: 96_000_000_000},
			}, nil)
		}},
		{"failed_nospace", func(t *testing.T) Model {
			return ended(t, stateFailed, extractengine.Outcome{Kind: extractengine.KindNoSpace, Exit: 3, CanResume: true, AutoResumed: true, AutoResumes: 4},
				func(m *Model) { m.autoResumes = 4 })
		}},
		{"failed_damaged", func(t *testing.T) Model {
			return ended(t, stateFailed, extractengine.Outcome{
				Kind: extractengine.KindDamaged, Exit: 4, CanResume: true,
				Error: `checksum mismatch in textures\level07.pak`,
			}, nil)
		}},

		// The same endings when the run left a log, at a path no machine has.
		{"done_log", func(t *testing.T) Model {
			return ended(t, stateDone, extractengine.Outcome{Kind: extractengine.KindDone, Elapsed: 6*time.Minute + 12*time.Second, LogPath: logFile},
				func(m *Model) { m.freeAfter, m.freeKnown = 15_700_000_000, true })
		}},
		{"paused_log", func(t *testing.T) Model {
			return ended(t, statePaused, extractengine.Outcome{Kind: extractengine.KindPaused, Exit: 130, CanResume: true, LogPath: logFile}, nil)
		}},
		{"failed_log", func(t *testing.T) Model {
			return ended(t, stateFailed, extractengine.Outcome{
				Kind: extractengine.KindFailed, Exit: 1, CanResume: true, LogPath: logFile,
				Error: `could not write textures\level07.pak: the file is in use by another program`,
			}, nil)
		}},
		{"details_log", func(t *testing.T) Model {
			return ended(t, stateFailed, extractengine.Outcome{
				Kind: extractengine.KindFailed, Exit: 1, CanResume: true, LogPath: logFile,
				Error: `could not write textures\level07.pak: the file is in use by another program`,
				Lines: []string{
					"Game-Backup.zip  84.2 GB zip, 96.0 GB uncompressed in 12408 entries",
					`error: could not write textures\level07.pak: the file is in use by another program`,
				},
			}, func(m *Model) { m.details = true })
		}},

		{"unfinished", func(t *testing.T) Model {
			return unfinished(newRig(t)).start().send(enter()).wantState("unfinished").m
		}},
		{"restore_confirm", func(t *testing.T) Model {
			return unfinished(newRig(t)).start().send(enter(), keyCode(tea.KeyDown), enter()).wantState("restoreConfirm").m
		}},
		{"restoring", func(t *testing.T) Model {
			m := unfinished(newRig(t)).start().send(enter()).wantState("unfinished").m
			m.state, m.restoreTook = stateRestoring, 12*time.Second
			return m
		}},
		{"restored", func(t *testing.T) Model {
			m := unfinished(newRig(t)).start().send(enter()).wantState("unfinished").m
			m.state = stateRestored
			return m
		}},
		{"details", func(t *testing.T) Model {
			return newRig(t, scripted(planNote("the archive and the folder are on the same drive"))).toCheck().key("l").m
		}},
	}
}

// TestGoldenFrames pins every state of the screen at 80x24 on the unicode
// tier, the tightest size the app draws in, and at 100x30 on the plain tier,
// so a terminal with no unicode still gets a card and a bar.
func TestGoldenFrames(t *testing.T) {
	seen := map[string]bool{}
	for _, st := range goldenStates() {
		if seen[st.name] {
			t.Fatalf("duplicate state %q", st.name)
		}
		seen[st.name] = true
		t.Run(st.name, func(t *testing.T) {
			m := st.build(t)
			requireGolden(t, "extract_"+st.name+"_80x24_unicode_nocolor", frameWithKeys(m, testCtx(80, 24, icons.TierUnicode)))
			requireGolden(t, "extract_"+st.name+"_100x30_ascii_nocolor", frameWithKeys(m, testCtx(100, 30, icons.TierASCII)))
		})
	}
}

// TestEveryFrameFitsItsWindow checks no state draws wider than the terminal
// or taller than the body, at either pinned size.
func TestEveryFrameFitsItsWindow(t *testing.T) {
	sizes := []struct {
		w, h int
		tier icons.Tier
	}{{80, 24, icons.TierUnicode}, {100, 30, icons.TierASCII}}
	for _, st := range goldenStates() {
		m := st.build(t)
		for _, size := range sizes {
			ctx := testCtx(size.w, size.h, size.tier)
			lines := strings.Split(strip(m.View(ctx)), "\n")
			if len(lines) > ctx.BodyHeight {
				t.Errorf("%s at %dx%d is %d lines tall, the body has %d", st.name, size.w, size.h, len(lines), ctx.BodyHeight)
			}
			for _, l := range lines {
				if w := len([]rune(l)); w > size.w {
					t.Errorf("%s at %dx%d has a line %d wide: %q", st.name, size.w, size.h, w, l)
				}
			}
		}
	}
}

// TestNoGoldenFrameContainsATypedArchivePassword guards the frames
// themselves (rule 79): the typed password must never be in any of them,
// whatever came after it was typed.
func TestNoGoldenFrameContainsATypedArchivePassword(t *testing.T) {
	passwordNeeded := needsPassword()
	// On top of every pinned state: the states a typed password is carried
	// through, from the field to the end of a run.
	after := []goldenState{
		{"password then plan", func(t *testing.T) Model {
			r := newRig(t, scripted(passwordNeeded, planFits()))
			r.start().send(enter()).until("password").typed(typedPw).send(enter()).until("check")
			return r.m
		}},
		{"password then details", func(t *testing.T) Model {
			r := newRig(t, scripted(passwordNeeded, planFits()))
			r.start().send(enter()).until("password").typed(typedPw).send(enter()).until("check").key("l")
			return r.m
		}},
		{"password then confirm", func(t *testing.T) Model {
			r := newRig(t, scripted(passwordNeeded, planFits()))
			r.start().send(enter()).until("password").typed(typedPw).send(enter()).until("check").send(enter()).wantState("confirm")
			return r.m
		}},
		{"password then run", func(t *testing.T) Model {
			r := newRig(t, scripted(passwordNeeded, planFits(), extractHangs()))
			r.start().send(enter()).until("password").typed(typedPw).send(enter()).until("check").send(enter()).key("y").wantState("running")
			return r.m
		}},
		{"password then paused", func(t *testing.T) Model {
			r := newRig(t, scripted(passwordNeeded, planFits(), extractHangs()))
			r.start().send(enter()).until("password").typed(typedPw).send(enter()).until("check").send(enter()).key("y").send(esc()).until("paused")
			return r.m
		}},
		{"password then done", func(t *testing.T) Model {
			r := newRig(t, scripted(passwordNeeded, planFits(), extractAll()))
			r.start().send(enter()).until("password").typed(typedPw).send(enter()).until("check").send(enter()).key("y").until("done")
			return r.m
		}},
	}
	for _, st := range append(goldenStates(), after...) {
		m := st.build(t)
		for _, ctx := range []struct {
			w, h int
			tier icons.Tier
		}{{80, 24, icons.TierUnicode}, {100, 30, icons.TierASCII}} {
			frame := frameWithKeys(m, testCtx(ctx.w, ctx.h, ctx.tier))
			if strings.Contains(frame, typedPw) {
				t.Errorf("state %q shows the typed password:\n%s", st.name, frame)
			}
		}
		if s := m.secret.String(); strings.Contains(s, typedPw) {
			t.Errorf("state %q: the secret prints the password", st.name)
		}
	}
}

// TestFramesDoNotDependOnTheOS checks the one row that differs by build, the
// file picker's Browse row, is pinned in its Windows form, and that an error
// from the operating system is never printed raw.
func TestFramesDoNotDependOnTheOS(t *testing.T) {
	pick := newRig(t, twoFound()).start()
	pick.wantView("Open the Windows file picker")
	if strings.Contains(pick.view(), "Only available on Windows") {
		t.Errorf("the Browse row depends on the build:\n%s", pick.view())
	}

	// A start that fails with an OS error shows hatchzip's side of it only
	// through the plain card; the picker never prints one at all.
	r := newRig(t, with(WithIdentify(func(string) (archive.Set, error) {
		return archive.Set{}, errors.New("createfile D:\\x.zip: the system cannot find the file specified")
	})))
	typeRow(r).typed(`D:\x.zip`).send(enter())
	r.wantView("Devpit could not read that file.")
	if strings.Contains(r.view(), "createfile") {
		t.Errorf("the picker printed a raw OS error:\n%s", r.view())
	}
}
