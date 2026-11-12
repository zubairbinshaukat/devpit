package filepicker_test

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/extract/extracttest"
	"github.com/zubairbinshaukat/devpit/internal/protect"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/filepicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

const (
	zipPath = `D:\Downloads\Game-Backup.zip`
	partOne = `D:\Downloads\dataset.7z.001`
)

func testContext() uictx.Context {
	return uictx.Context{
		Theme: theme.For(true), Icons: icons.Unicode(), Config: config.Default(),
		Width: 80, Height: 24, BodyHeight: 18, BodyTop: 4,
	}
}

func set(first string, bytes int64, parts int) archive.Set {
	f, _, _, _ := archive.Classify(first)
	s := archive.Set{Picked: first, First: first, Format: f, Bytes: bytes}
	for n := 1; n <= parts; n++ {
		p, _ := archive.Volume(first, n)
		s.Parts = append(s.Parts, p)
	}
	return s
}

// identify is a fake extract.Identify: the naming rules, a network refusal
// and a table of files that exist.
func identify(exists ...string) filepicker.IdentifyFunc {
	return func(path string) (archive.Set, error) {
		if strings.HasPrefix(path, `\\`) {
			return archive.Set{}, extract.ErrNetworkPath
		}
		_, first, ok, err := archive.Classify(path)
		if !ok {
			return archive.Set{}, err
		}
		for _, e := range exists {
			if strings.EqualFold(e, first) {
				return set(first, 5_000_000_000, 1), nil
			}
		}
		return archive.Set{}, &fs.PathError{Op: "lstat", Path: first, Err: fs.ErrNotExist}
	}
}

func now() time.Time { return time.Date(2026, 1, 4, 3, 4, 5, 0, time.UTC) }

func picker(exists ...string) filepicker.Model {
	return filepicker.New(identify(exists...)).
		WithClock(now).
		WithBrowseAvailable(true).
		WithBrowse(func(string) (string, error) { return "", nil })
}

func press(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: []rune(s)[0], Text: s} }

func code(c rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: c} }

// drive feeds a message in and then every message its commands produce, the
// way the owning screen routes the picker's own results back to it. It
// returns what came out that the picker did not consume.
func drive(t *testing.T, m filepicker.Model, msg tea.Msg) (filepicker.Model, []tea.Msg) {
	t.Helper()
	var out []tea.Msg
	queue := []tea.Msg{msg}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 100 {
			t.Fatal("the picker's commands never ended")
		}
		next, cmd := m.Update(queue[0])
		queue = queue[1:]
		m = next
		if cmd == nil {
			continue
		}
		switch res := cmd().(type) {
		case nil:
		case filepicker.BrowsedMsg, filepicker.CheckedMsg:
			queue = append(queue, res)
		default:
			out = append(out, res)
		}
	}
	return m, out
}

func typeIn(t *testing.T, m filepicker.Model, s string) filepicker.Model {
	t.Helper()
	for _, r := range s {
		m, _ = drive(t, m, press(string(r)))
	}
	return m
}

// toTyping moves to the "Type a path…" row and opens it.
func toTyping(t *testing.T, m filepicker.Model) filepicker.Model {
	t.Helper()
	for range m.Rows() {
		m, _ = drive(t, m, code(tea.KeyDown))
	}
	m, _ = drive(t, m, code(tea.KeyEnter))
	if !m.Typing() {
		t.Fatalf("the picker is not typing:\n%s", ansi.Strip(m.View(testContext())))
	}
	return m
}

func chosen(t *testing.T, out []tea.Msg) archive.Set {
	t.Helper()
	for _, m := range out {
		if c, ok := m.(filepicker.ChosenMsg); ok {
			return c.Set
		}
	}
	t.Fatalf("no ChosenMsg came out: %v", out)
	return archive.Set{}
}

func TestChoosingAListedArchiveEmitsItsSet(t *testing.T) {
	m := picker().SetRows([]filepicker.Row{
		{Set: set(zipPath, 84_200_000_000, 1)},
		{Set: set(partOne, 41_700_000_000, 11)},
	})
	view := ansi.Strip(m.View(testContext()))
	for _, want := range []string{
		"Which archive do you want to extract?",
		zipPath, "84.2 GB · in Downloads",
		partOne + "  (+10 parts)", "41.7 GB · in Downloads",
		"Type a path…", "Browse…", "Open the Windows file picker",
		"Listed: archives of 1 GB or more in Downloads and your recent folders.",
		"Works with .zip, .7z, .rar and split parts (.001, .part1.rar, .r00).",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the picker does not say %q:\n%s", want, view)
		}
	}

	m, _ = drive(t, m, code(tea.KeyDown))
	_, out := drive(t, m, code(tea.KeyEnter))
	got := chosen(t, out)
	if got.First != partOne || len(got.Parts) != 11 {
		t.Errorf("chose %+v, want the whole split set", got)
	}
}

func TestUnfinishedRowsSayWhenTheyStarted(t *testing.T) {
	m := picker().SetRows([]filepicker.Row{
		{Set: set(zipPath, 34_000_000_000, 1), Unfinished: true, Started: now().Add(-49 * time.Hour)},
		{Set: set(partOne, 41_700_000_000, 11), Unfinished: true},
	})
	view := ansi.Strip(m.View(testContext()))
	for _, want := range []string{"Unfinished · 34.0 GB · started 2 days ago", "Unfinished · 41.7 GB"} {
		if !strings.Contains(view, want) {
			t.Errorf("the picker does not say %q:\n%s", want, view)
		}
	}
}

func TestTypedPathIsIdentifiedBeforeItIsChosen(t *testing.T) {
	m := toTyping(t, picker(zipPath))
	// Any part of a set picks the set, and quotes from "Copy as path" go.
	m = typeIn(t, m, `"D:\Downloads\Game-Backup.zip"`)
	m, out := drive(t, m, code(tea.KeyEnter))
	if got := chosen(t, out); got.First != zipPath {
		t.Errorf("chose %q", got.First)
	}
	if m.Refusal() != "" {
		t.Errorf("a good path left the refusal %q", m.Refusal())
	}

	// Nothing typed is refused without asking the disk.
	e := toTyping(t, filepicker.New(func(string) (archive.Set, error) {
		t.Error("an empty path was identified")
		return archive.Set{}, nil
	}).WithBrowseAvailable(true))
	e, out = drive(t, e, code(tea.KeyEnter))
	if len(out) != 0 || e.Refusal() != "Type the path of an archive first." {
		t.Errorf("an empty path: %v, %q", out, e.Refusal())
	}
}

// TestRefusalsAreOneSentence is rule 82 as the user meets it: every refused
// path gets one plain line under the list, never a raw error, and nothing is
// chosen.
func TestRefusalsAreOneSentence(t *testing.T) {
	typed := map[string]string{
		`\\nas\backups\big.zip`:     "Network paths are not extracted. Copy the archive to this PC first.",
		`D:\Downloads\big.z01`:      "Split ZIP (.z01) is not supported by hatchzip. Join it with 7-Zip first.",
		`D:\Downloads\notes.txt`:    "That is not an archive Devpit can extract (.zip, .7z, .rar).",
		`D:\Downloads\missing.zip`:  "There is no file at that path.",
		`D:\Downloads\holiday pics`: "That is not an archive Devpit can extract (.zip, .7z, .rar).",
	}
	for path, want := range typed {
		m := toTyping(t, picker(zipPath))
		m = typeIn(t, m, path)
		m, out := drive(t, m, code(tea.KeyEnter))
		if len(out) != 0 {
			t.Errorf("%s: something was emitted for a refused path: %v", path, out)
		}
		if got := m.Refusal(); got != want {
			t.Errorf("%s: refusal = %q, want %q", path, got, want)
		}
		view := ansi.Strip(m.View(testContext()))
		if !strings.Contains(view, "✗ "+want) {
			t.Errorf("%s: the refusal is not on screen:\n%s", path, view)
		}
	}

	// Every reason the engine can give, wrapped the way it wraps them, plus
	// one it has never heard of: one line, a sentence, no error plumbing.
	reasons := []error{
		filepicker.ErrEmptyPath,
		extract.ErrNetworkPath,
		fmt.Errorf("%w: %s", extract.ErrProtected, `C:\Windows`),
		extract.ErrReparsePoint,
		extract.ErrNotAFile,
		extract.ErrFirstPartMissing,
		archive.ErrSplitZip,
		archive.ErrNotArchive,
		&fs.PathError{Op: "lstat", Path: `D:\x.zip`, Err: fs.ErrNotExist},
		&fs.PathError{Op: "lstat", Path: `D:\x.zip`, Err: fs.ErrPermission},
		errors.New("createfile D:\\x.zip: the parameter is incorrect"),
	}
	for _, err := range reasons {
		got := filepicker.Refusal(err)
		switch {
		case got == "":
			t.Errorf("no sentence for %v", err)
		case strings.ContainsAny(got, "\n\r"):
			t.Errorf("the refusal for %v is more than one line: %q", err, got)
		case ansi.StringWidth(got) > 76:
			t.Errorf("the refusal for %v does not fit an 80-column window: %q", err, got)
		case !strings.HasSuffix(got, "."):
			t.Errorf("the refusal for %v is not a sentence: %q", err, got)
		}
		for _, raw := range []string{"lstat", "createfile", "%!", "<nil>", `D:\x.zip`} {
			if strings.Contains(got, raw) {
				t.Errorf("the refusal for %v shows error plumbing (%q): %q", err, raw, got)
			}
		}
	}
	if got := filepicker.Refusal(nil); got != "" {
		t.Errorf("no error gave the refusal %q", got)
	}
}

// TestAMissingFileIsNotCalledAFolder types paths through the engine's own
// Identify, not a fake of it: the engine reports a path with nothing at it as
// "not a regular file" too, and the picker must still say that nothing is
// there, and keep "folder" for a folder.
func TestAMissingFileIsNotCalledAFolder(t *testing.T) {
	fsys := extracttest.NewFakeFS()
	fsys.AddFile(zipPath, 5_000_000_000)
	fsys.AddDir(`D:\Downloads\folder.zip`)
	fsys.AddFile(`D:\Downloads\notes.txt`, 10)
	fsys.AddFile(`D:\Downloads\alone.7z.002`, 10)
	engine := func(path string) (archive.Set, error) {
		return extract.Identify(path, fsys, protect.List{}, nil)
	}
	for path, want := range map[string]string{
		`D:\Downloads\missing.zip`:       "There is no file at that path.",
		`D:\Nowhere\missing.zip`:         "There is no file at that path.",
		`"D:\Downloads\missing.7z.001"`:  "There is no file at that path.",
		`D:\Downloads\missing.part2.rar`: "There is no file at that path.",
		`D:\Downloads\folder.zip`:        "That is a folder, not an archive file.",
		`D:\Downloads\notes.txt`:         "That is not an archive Devpit can extract (.zip, .7z, .rar).",
		`D:\Downloads\missing-notes.txt`: "That is not an archive Devpit can extract (.zip, .7z, .rar).",
		`D:\Downloads\alone.7z.002`:      "A part of that archive is missing. Put every part in the same folder.",
	} {
		m := toTyping(t, filepicker.New(engine).WithClock(now).WithBrowseAvailable(true))
		m = typeIn(t, m, path)
		m, out := drive(t, m, code(tea.KeyEnter))
		if len(out) != 0 {
			t.Errorf("%s: something was emitted for a refused path: %v", path, out)
		}
		if got := m.Refusal(); got != want {
			t.Errorf("%s: refusal = %q, want %q", path, got, want)
		}
	}

	m := toTyping(t, filepicker.New(engine).WithClock(now).WithBrowseAvailable(true))
	m = typeIn(t, m, zipPath)
	if _, out := drive(t, m, code(tea.KeyEnter)); chosen(t, out).First != zipPath {
		t.Error("the archive that is there was not chosen")
	}
}

// TestBrowseResultIsRoutedBack: the file picker's result comes back as a
// BrowsedMsg the owning screen hands to Update. A picked file is identified
// like a typed one; a cancel changes nothing; a failure is a plain line.
func TestBrowseResultIsRoutedBack(t *testing.T) {
	browseTo := func(path string, err error) (filepicker.Model, tea.Cmd) {
		m := picker(zipPath).WithBrowse(func(string) (string, error) { return path, err })
		m, _ = drive(t, m, code(tea.KeyDown)) // Type a path… → Browse…
		return m.Update(code(tea.KeyEnter))
	}

	// The command's result is a BrowsedMsg, and until it is routed back the
	// picker says it is waiting.
	m, cmd := browseTo(zipPath, nil)
	if cmd == nil {
		t.Fatal("Browse produced no command")
	}
	if view := ansi.Strip(m.View(testContext())); !strings.Contains(view, "Waiting for the Windows file picker…") {
		t.Errorf("the picker does not say it is waiting:\n%s", view)
	}
	res := cmd()
	if _, ok := res.(filepicker.BrowsedMsg); !ok {
		t.Fatalf("Browse produced %T, want BrowsedMsg", res)
	}
	m, out := drive(t, m, res)
	if got := chosen(t, out); got.First != zipPath {
		t.Errorf("the browsed file chose %q", got.First)
	}
	if view := ansi.Strip(m.View(testContext())); strings.Contains(view, "Waiting") {
		t.Errorf("the picker is still waiting after the result:\n%s", view)
	}

	// A browsed file goes through the same checks as a typed one.
	m, cmd = browseTo(`\\nas\backups\big.zip`, nil)
	m, out = drive(t, m, cmd())
	if len(out) != 0 || m.Refusal() != "Network paths are not extracted. Copy the archive to this PC first." {
		t.Errorf("a browsed network path: %v, %q", out, m.Refusal())
	}

	// Cancelling the dialog is not an error and chooses nothing.
	m, cmd = browseTo("", nil)
	m, out = drive(t, m, cmd())
	if len(out) != 0 || m.Refusal() != "" {
		t.Errorf("a cancelled dialog: %v, %q", out, m.Refusal())
	}

	// A dialog that could not open says so plainly.
	m, cmd = browseTo("", errors.New("exec: powershell.exe: file not found"))
	m, out = drive(t, m, cmd())
	if len(out) != 0 || strings.Contains(m.Refusal(), "exec") || m.Refusal() == "" {
		t.Errorf("a failed dialog: %v, %q", out, m.Refusal())
	}
}

func TestOnlyListsTheGivenRows(t *testing.T) {
	m := picker().Only([]filepicker.Row{{Set: set(zipPath, 62_400_000_000, 1)}})
	view := ansi.Strip(m.View(testContext()))
	for _, gone := range []string{"Type a path", "Browse"} {
		if strings.Contains(view, gone) {
			t.Errorf("a limited picker offers %q:\n%s", gone, view)
		}
	}
	_, out := drive(t, m, code(tea.KeyEnter))
	if got := chosen(t, out); got.First != zipPath {
		t.Errorf("chose %q", got.First)
	}
}

func TestBrowseRowIsDisabledWhereItCannotWork(t *testing.T) {
	m := picker().WithBrowseAvailable(false)
	view := ansi.Strip(m.View(testContext()))
	if !strings.Contains(view, "Only available on Windows") {
		t.Errorf("the Browse row does not say it is unavailable:\n%s", view)
	}
	m, _ = drive(t, m, code(tea.KeyDown)) // a disabled row is skipped
	_, cmd := m.Update(code(tea.KeyEnter))
	if cmd != nil {
		if _, ok := cmd().(filepicker.BrowsedMsg); ok {
			t.Error("a disabled Browse row opened the dialog")
		}
	}
}

func TestEscCancels(t *testing.T) {
	_, out := drive(t, picker(), code(tea.KeyEscape))
	if len(out) != 1 {
		t.Fatalf("Esc produced %v", out)
	}
	if _, ok := out[0].(filepicker.CancelledMsg); !ok {
		t.Errorf("Esc produced %T", out[0])
	}
}

func TestAManyRowListLeavesRoomForItsNotes(t *testing.T) {
	var rows []filepicker.Row
	for i := range 9 {
		rows = append(rows, filepicker.Row{Set: set(fmt.Sprintf(`D:\Downloads\backup-%02d.zip`, i), 2_000_000_000, 1)})
	}
	m := picker().SetRows(rows).SetRefusal("That is not an archive Devpit can extract (.zip, .7z, .rar).")
	ctx := testContext()
	lines := strings.Split(ansi.Strip(m.View(ctx)), "\n")
	if len(lines) > ctx.BodyHeight {
		t.Errorf("the picker is %d lines tall in a body of %d:\n%s", len(lines), ctx.BodyHeight, strings.Join(lines, "\n"))
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "not an archive") {
		t.Errorf("the refusal is not the last line: %q", last)
	}
}

func TestFormatSizeIsHatchzipsStyle(t *testing.T) {
	for n, want := range map[int64]string{
		0:               "0 B",
		999:             "999 B",
		1_000:           "1.0 KB",
		401_400:         "401.4 KB",
		1_000_000:       "1.0 MB",
		84_200_000_000:  "84.2 GB",
		112_500_000_000: "112.5 GB",
		999_960_000_000: "1.0 TB",
		-5:              "0 B",
	} {
		if got := filepicker.FormatSize(n); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestAgo(t *testing.T) {
	for d, want := range map[time.Duration]string{
		20 * time.Second: "just now",
		time.Minute:      "1 minute ago",
		90 * time.Minute: "1 hour ago",
		47 * time.Hour:   "47 hours ago",
		49 * time.Hour:   "2 days ago",
	} {
		if got := filepicker.Ago(d); got != want {
			t.Errorf("Ago(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestBaseAndDirSplitWindowsPathsOnAnyOS(t *testing.T) {
	if got := filepicker.Base(zipPath); got != "Game-Backup.zip" {
		t.Errorf("Base = %q", got)
	}
	if got := filepicker.Base(filepicker.Dir(zipPath)); got != "Downloads" {
		t.Errorf("the folder's name = %q", got)
	}
	if got := filepicker.Dir("archive.zip"); got != "" {
		t.Errorf("Dir of a bare name = %q", got)
	}
}
