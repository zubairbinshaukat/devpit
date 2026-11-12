package share

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/extract/extracttest"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/share/robocopy"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	extractscreen "github.com/zubairbinshaukat/devpit/internal/ui/screens/extract"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The archive the receives of these tests bring.
const (
	backupPath  = `D:\Games\Backup.7z`
	backupBytes = int64(62_400_000_000)
)

// bigArchive is one single-file archive the search found.
func bigArchive(path string, size int64) extract.Found {
	f, first, ok, err := archive.Classify(path)
	if !ok {
		panic("bigArchive: " + path + ": " + err.Error())
	}
	return extract.Found{
		Set: archive.Set{Picked: path, First: first, Parts: []string{first}, Format: f, Bytes: size},
		Mod: extracttest.Epoch(),
	}
}

// finishReceive drives a receive of 80 GB in 1,200 files into D:\Games from
// the address to the done card, the way a person would: every step is a real
// message, and the archive search runs as the screen's own command.
func finishReceive(t *testing.T, h *harness, r *fakeReceiver) *harness {
	t.Helper()
	r.check = recv.Check{Plan: robocopy.Plan{Files: 1200, Bytes: 80 << 30, SummaryFound: true}, FreeBytes: 500 << 30, FileSystem: "NTFS"}
	r.outcome = recv.Outcome{Done: true, Message: "All files were copied.", Elapsed: 14*time.Minute + 2*time.Second}
	h.typed("192.168.1.5").send(enter(), enter(), enter(), enter(), chosen(`D:\Games`), enter())
	if s := asRecv(h); s.stage != recvDone {
		t.Fatalf("stage %d, want the done card\n%s", s.stage, h.view())
	}
	return h
}

// received is a finished receive whose destination holds these archives.
func received(t *testing.T, found ...extract.Found) (*harness, *fakeReceiver) {
	t.Helper()
	r := newRecvFake()
	r.archives = found
	return finishReceive(t, recvIn(t, r), r), r
}

// doneWithArchives is the done card of such a receive, for the goldens.
func doneWithArchives(t *testing.T, found ...extract.Found) uictx.Screen {
	t.Helper()
	h, _ := received(t, found...)
	return h.scr
}

// hints is the footer of a screen as text.
func hints(scr uictx.Screen) string {
	var out []string
	for _, b := range scr.ShortHelp() {
		out = append(out, b.Help().Key+" "+b.Help().Desc)
	}
	return strings.Join(out, " · ")
}

func TestDoneOffersToExtractABigArchive(t *testing.T) {
	h, r := received(t, bigArchive(backupPath, backupBytes))
	if !slices.Equal(r.findDirs, []string{`D:\Games`}) {
		t.Fatalf("searched %v, want the destination once", r.findDirs)
	}
	v := h.view()
	for _, want := range []string{
		"Done. 80.0 GB copied", `The files are in D:\Games`,
		"You received a big archive: Backup.7z  ·  62.4 GB",
		"Devpit can extract it in place, even when the disk is nearly full.",
		"Press x to extract it (you see the space plan first), Enter to go back.",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("the done card does not say %q:\n%s", want, v)
		}
	}
	if got := hints(h.scr); got != "x extract archive · enter back" {
		t.Errorf("keys = %q", got)
	}
	if h.scr.(uictx.BusyReporter).Busy() {
		t.Error("the done card is busy, so Esc would not leave it")
	}

	// A split set is one offer, named by its first volume, with its parts.
	parts := []string{`D:\Games\set.7z.001`, `D:\Games\set.7z.002`, `D:\Games\set.7z.003`}
	split := extract.Found{Set: archive.Set{Picked: parts[0], First: parts[0], Parts: parts, Format: archive.Format7z, Bytes: 1_200_000_000}}
	h, _ = received(t, split)
	if v := h.view(); !strings.Contains(v, "You received a big archive: set.7z.001  ·  1.2 GB in 3 parts") {
		t.Errorf("split set:\n%s", v)
	}

	// Enter still goes back.
	h.send(enter())
	if h.popped != 1 || len(h.pushed) != 0 {
		t.Errorf("Enter popped %d and pushed %d", h.popped, len(h.pushed))
	}
}

// The threshold is the engine's, on the sum of a set's parts: the screen's
// real search over a disk with only small archives offers nothing, and one
// more part that takes a set over 1 GB is offered.
func TestDoneOffersNothingBelowTheThreshold(t *testing.T) {
	fsys := extracttest.NewFakeFS()
	fsys.AddFile(`D:\Games\small.zip`, extract.OfferMinBytes-1)
	fsys.AddFile(`D:\Games\Data\notes.7z`, 40_000)
	fsys.AddFile(`D:\Games\Data\level01.pak`, 30_000_000_000) // big, but no archive
	fsys.AddFile(`D:\Games\set.7z.001`, 400_000_000)
	fsys.AddFile(`D:\Games\set.7z.002`, 400_000_000)

	receive := func() *harness {
		r := newRecvFake()
		h := recvIn(t, r)
		s := asRecv(h)
		s.findArchives = findIn(fsys)
		h.scr = s
		return finishReceive(t, h, r)
	}

	h := receive()
	if s := asRecv(h); len(s.archives) != 0 {
		t.Fatalf("offered %+v", s.archives)
	}
	if v := h.view(); strings.Contains(v, "archive") || !strings.Contains(v, "Press Enter to go back.") {
		t.Errorf("the done card changed:\n%s", v)
	}
	if got := hints(h.scr); got != "enter back" {
		t.Errorf("keys = %q", got)
	}
	// x is not a key here.
	h.send(text("x"))
	if len(h.pushed) != 0 || h.popped != 0 {
		t.Errorf("x pushed %d and popped %d with nothing to extract", len(h.pushed), h.popped)
	}

	fsys.AddFile(`D:\Games\set.7z.003`, 400_000_000)
	h = receive()
	if v := h.view(); !strings.Contains(v, "You received a big archive: set.7z.001  ·  1.2 GB in 3 parts") {
		t.Errorf("a set over the threshold is not offered:\n%s", v)
	}
}

// extractFakes builds the extract screen over the extracttest fakes instead
// of this PC's hatchzip and disk, keeping the options the share screen chose.
func extractFakes(fakes *extracttest.Fakes, deps extract.Deps) func(opts ...extractscreen.Option) uictx.Screen {
	now := extracttest.Epoch().Add(48 * time.Hour)
	identify := func(path string) (archive.Set, error) {
		f, first, ok, err := archive.Classify(path)
		if !ok {
			return archive.Set{}, err
		}
		fi, err := fakes.FS.Lstat(first)
		if err != nil {
			return archive.Set{}, err
		}
		return archive.Set{Picked: path, First: first, Parts: []string{first}, Format: f, Bytes: fi.Size()}, nil
	}
	return func(opts ...extractscreen.Option) uictx.Screen {
		all := []extractscreen.Option{
			extractscreen.WithExtractor(extract.New(extracttest.Tool(), deps)),
			extractscreen.WithDetect(func(context.Context) tools.Tool {
				return tools.Tool{Name: "hatchzip", Exe: "hatchzip", Path: extracttest.ToolPath, Version: extracttest.ToolVersion, Found: true}
			}),
			extractscreen.WithScoop(func(context.Context) tools.Tool { return tools.Tool{} }),
			extractscreen.WithClock(func() time.Time { now = now.Add(time.Second); return now }),
			extractscreen.WithIdentify(identify),
			extractscreen.WithFind(func(context.Context, []string) []extract.Found { return nil }),
			extractscreen.WithStatFS(fakes.FS),
			extractscreen.WithOutDir(func(set archive.Set) string { return strings.TrimSuffix(set.First, ".7z") }),
			extractscreen.WithFree(func(string) (uint64, bool) { return 15_700_000_000, true }),
			extractscreen.WithBrowse(func(string) (string, error) { return "", nil }),
		}
		return extractscreen.New(append(all, opts...)...)
	}
}

// driveExtract runs a pushed extract screen as the program loop would, moving
// the fake clock, until it reaches a step.
func driveExtract(t *testing.T, scr uictx.Screen, clock *extracttest.Clock, state string) extractscreen.Model {
	t.Helper()
	ctx := testCtx(80, 24, icons.TierUnicode)
	inbox := make(chan tea.Msg, 1024)
	run := func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			if m := c(); m != nil {
				inbox <- m
			}
		}()
	}
	t.Cleanup(func() {
		if s, ok := scr.(uictx.Stopper); ok {
			s.Stop()
		}
	})
	run(scr.Init())
	deadline := time.Now().Add(10 * time.Second)
	for {
		m := scr.(extractscreen.Model)
		if m.StateName() == state {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatalf("the extract screen never reached %q; it is on %q\n%s", state, m.StateName(), strip(m.View(ctx)))
		}
		select {
		case msg := <-inbox:
			switch b := msg.(type) {
			case tea.BatchMsg:
				for _, c := range b {
					run(c)
				}
			case uictx.PushScreenMsg, uictx.PopScreenMsg, uictx.StatusMsg:
			default:
				next, cmd := scr.Update(msg, ctx)
				scr = next
				run(cmd)
			}
		default:
			clock.Advance(250 * time.Millisecond)
			time.Sleep(time.Millisecond)
		}
	}
}

// ran counts the hatchzip runs of one kind.
func ran(fakes *extracttest.Fakes, word string) int {
	n := 0
	for _, s := range fakes.Runner.Specs() {
		if len(s.Args) > 0 && s.Args[0] == word {
			n++
		}
	}
	return n
}

// TestXOpensTheExtractCheckAndStartsNothing is safety rule 89 at the share
// end: x pushes the extract screen on the archive, which runs the space check
// and stops there. No extraction is started by the offer.
func TestXOpensTheExtractCheckAndStartsNothing(t *testing.T) {
	deps, fakes := extracttest.NewDeps(extracttest.Check(0,
		extracttest.Plan("Backup.7z", "62.4 GB", "7z", "96.0 GB", 12408, "31.5 GB", "96.0 GB", "13.8 GB", "15.7 GB")))
	fakes.FS.AddFile(backupPath, backupBytes)

	h, r := received(t, bigArchive(backupPath, backupBytes))
	s := asRecv(h)
	s.newExtract = extractFakes(fakes, deps)
	h.scr = s
	runs := r.called("run")

	h.send(text("x"))
	if len(h.pushed) != 1 || h.popped != 0 {
		t.Fatalf("x pushed %d screens and popped %d", len(h.pushed), h.popped)
	}
	pushed, ok := h.pushed[0].(extractscreen.Model)
	if !ok {
		t.Fatalf("x pushed a %T, want the extract screen", h.pushed[0])
	}
	if pushed.StateName() != "checking" {
		t.Fatalf("the extract screen opens on %q, want its check", pushed.StateName())
	}
	if n := len(fakes.Runner.Specs()); n != 0 {
		t.Fatalf("pressing x ran hatchzip %d times before the screen was even shown", n)
	}
	// The done card stays underneath, as it was.
	if asRecv(h).stage != recvDone || r.called("run") != runs {
		t.Errorf("the receive screen moved: stage %d, runs %d", asRecv(h).stage, r.called("run"))
	}

	m := driveExtract(t, pushed, fakes.Clock, "check")
	if v := strip(m.View(testCtx(80, 24, icons.TierUnicode))); !strings.Contains(v, "Backup.7z") {
		t.Errorf("the check is not about the received archive:\n%s", v)
	}
	if ran(fakes, "check") != 1 || ran(fakes, "extract") != 0 || len(fakes.Runner.Specs()) != 1 {
		t.Fatalf("hatchzip runs = %+v, want one check and nothing else", fakes.Runner.Specs())
	}
	if m.Busy() || fakes.Awake.Total() != 0 {
		t.Errorf("something is running on the check card: busy %v, keep-awake holds %d", m.Busy(), fakes.Awake.Total())
	}
}

func TestSeveralArchivesOpenThePickerWithOnlyThem(t *testing.T) {
	deps, fakes := extracttest.NewDeps()
	found := []extract.Found{
		bigArchive(backupPath, backupBytes),
		bigArchive(`D:\Games\Mods\Textures.zip`, 81_300_000_000),
		bigArchive(`D:\Games\Saves.rar`, 4_300_000_000),
	}
	h, _ := received(t, found...)
	v := h.view()
	for _, want := range []string{
		"You received 3 big archives (148.0 GB).",
		"Press x to choose one (you see the space plan first), Enter to go back.",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("the done card does not say %q:\n%s", want, v)
		}
	}
	if got := hints(h.scr); got != "x extract archive · enter back" {
		t.Errorf("keys = %q", got)
	}

	s := asRecv(h)
	s.newExtract = extractFakes(fakes, deps)
	h.scr = s
	h.send(text("x"))
	if len(h.pushed) != 1 {
		t.Fatalf("x pushed %d screens", len(h.pushed))
	}
	m := driveExtract(t, h.pushed[0], fakes.Clock, "pick")
	// Let the screen take its first message, so the list is the one it shows.
	list := strip(m.View(testCtx(100, 30, icons.TierUnicode)))
	for _, f := range found {
		if !strings.Contains(list, f.Set.First) {
			t.Errorf("the list lacks %s:\n%s", f.Set.First, list)
		}
	}
	for _, hidden := range []string{"Type a path", "Browse"} {
		if strings.Contains(list, hidden) {
			t.Errorf("the list offers %q, so it is not only the received archives:\n%s", hidden, list)
		}
	}
	if n := len(fakes.Runner.Specs()); n != 0 {
		t.Errorf("opening the list ran hatchzip %d times", n)
	}
}

// A search that is not back in time is dropped: no offer, no error, and the
// done card is the plain one. That holds for a search that stops when told
// (and had found something by then) and for one that never comes back.
func TestASlowSearchShowsNoOffer(t *testing.T) {
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	searches := map[string]func(ctx context.Context, dir string) []extract.Found{
		"stops late with a result": func(ctx context.Context, _ string) []extract.Found {
			<-ctx.Done()
			return []extract.Found{bigArchive(backupPath, backupBytes)}
		},
		"never answers": func(context.Context, string) []extract.Found {
			<-stuck
			return []extract.Found{bigArchive(backupPath, backupBytes)}
		},
	}
	for name, search := range searches {
		t.Run(name, func(t *testing.T) {
			r := newRecvFake()
			h := recvIn(t, r)
			s := asRecv(h)
			s.findArchives, s.findLimit = search, 5*time.Millisecond
			h.scr = s
			finishReceive(t, h, r)

			if s := asRecv(h); len(s.archives) != 0 || s.err != nil {
				t.Fatalf("archives %+v, err %v", s.archives, s.err)
			}
			if v := h.view(); strings.Contains(v, "archive") || !strings.Contains(v, "Press Enter to go back.") {
				t.Errorf("the done card changed:\n%s", v)
			}
			if got := hints(h.scr); got != "enter back" {
				t.Errorf("keys = %q", got)
			}
			h.send(text("x"))
			if len(h.pushed) != 0 {
				t.Error("x opened the extract screen with no offer on screen")
			}
		})
	}
}

// A result that arrives for another folder, or after the card is gone, is
// not shown.
func TestAnArchiveResultForAnotherCopyIsDropped(t *testing.T) {
	h, _ := received(t)
	h.send(archivesMsg{dir: `E:\Elsewhere`, found: []extract.Found{bigArchive(backupPath, backupBytes)}})
	if len(asRecv(h).archives) != 0 {
		t.Error("a result for another folder is on the done card")
	}
	s := asRecv(h)
	s.stage = recvFailed
	h.scr = s
	h.send(archivesMsg{dir: `D:\Games`, found: []extract.Found{bigArchive(backupPath, backupBytes)}})
	if len(asRecv(h).archives) != 0 {
		t.Error("a result arrived off the done card and was kept")
	}
}

// TestShareRecvDoneIsUnchangedWithoutArchives holds the done card of a
// receive that brought no big archive to the frames it had before the offer
// existed. It reads the goldens itself, so -update cannot rewrite them here,
// and it gets to the card through a real receive and a real (empty) search.
func TestShareRecvDoneIsUnchangedWithoutArchives(t *testing.T) {
	frames := []struct {
		golden string
		ctx    uictx.Context
	}{
		{"share_recv_done_80x24_unicode_nocolor", testCtx(80, 24, icons.TierUnicode)},
		{"share_recv_done_100x30_ascii_nocolor", testCtx(100, 30, icons.TierASCII)},
	}
	h, r := received(t)
	if len(r.findDirs) != 1 {
		t.Fatalf("the search ran %d times, want once", len(r.findDirs))
	}
	for _, f := range frames {
		want, err := os.ReadFile(filepath.Join(goldenDir, f.golden+".golden"))
		if err != nil {
			t.Fatal(err)
		}
		if got := normalise(frameWithKeys(h.scr, f.ctx)); got != normalise(string(want)) {
			t.Errorf("the done card no longer matches %s\n--- want ---\n%s\n--- got ---\n%s", f.golden, want, got)
		}
	}
}
