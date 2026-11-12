package extract_test

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/extract/extracttest"
	"github.com/zubairbinshaukat/devpit/internal/protect"
)

func sysEnv(k string) string {
	return map[string]string{
		"WINDIR": `C:\Windows`, "SystemRoot": `C:\Windows`, "ProgramFiles": `C:\Program Files`,
		"ProgramFiles(x86)": `C:\Program Files (x86)`, "ProgramData": `C:\ProgramData`,
	}[k]
}

// Safety rule 82: UNC paths in every spelling are refused before any I/O.
func TestIdentifyRefusesNetworkPaths(t *testing.T) {
	fsys := extracttest.NewFakeFS()
	for _, p := range []string{`\\server\share\Game.zip`, `//server/share/Game.zip`, `\\?\UNC\server\share\Game.zip`, `\\.\UNC\server\share\Game.zip`} {
		fsys.AddFile(p, 5_000_000_000)
		if _, err := extract.Identify(p, fsys, protect.List{}, sysEnv); !errors.Is(err, extract.ErrNetworkPath) {
			t.Errorf("Identify(%q) = %v", p, err)
		}
	}
	set := archive.Set{Picked: `D:\a.zip`, First: `D:\a.zip`, Parts: []string{`D:\a.zip`, `\\nas\x\a.z`}}
	fsys.AddFile(`D:\a.zip`, 1)
	if err := extract.Validate(set, fsys, protect.List{}, sysEnv); !errors.Is(err, extract.ErrNetworkPath) {
		t.Errorf("a network part: %v", err)
	}
}

// Safety rule 82: system folders and login or account folders are refused.
func TestIdentifyRefusesProtectedPlaces(t *testing.T) {
	fsys := extracttest.NewFakeFS()
	for _, p := range []string{
		`C:\Windows\Temp\big.zip`, `c:\windows\big.7z`, `C:\Program Files\App\data.zip`,
		`C:\Program Files (x86)\Game\pack.rar`, `C:\ProgramData\cache.zip`,
	} {
		fsys.AddFile(p, 2_000_000_000)
		if _, err := extract.Identify(p, fsys, protect.List{}, sysEnv); !errors.Is(err, extract.ErrProtected) {
			t.Errorf("Identify(%q) = %v", p, err)
		}
	}
	// A login folder from protect.Default, on a real absolute path of this OS.
	home := t.TempDir()
	prot := protect.Default(func(k string) (string, bool) {
		if k == "USERPROFILE" {
			return home, true
		}
		return "", false
	})
	keys := filepath.Join(home, ".ssh", "backup.zip")
	fsys.AddFile(keys, 10)
	if _, err := extract.Identify(keys, fsys, prot, sysEnv); !errors.Is(err, extract.ErrProtected) {
		t.Errorf("an archive in .ssh: %v", err)
	}
	ok := filepath.Join(home, "Downloads", "fine.zip")
	fsys.AddFile(ok, 10)
	if _, err := extract.Identify(ok, fsys, prot, sysEnv); err != nil {
		t.Errorf("an ordinary archive: %v", err)
	}
}

func TestIdentifyNamesAndParts(t *testing.T) {
	fsys := extracttest.NewFakeFS()
	for _, p := range []string{`D:\dl\set.7z.001`, `D:\dl\set.7z.002`, `D:\dl\set.7z.003`} {
		fsys.AddFile(p, 1_000)
	}
	set, err := extract.Identify(`D:\dl\set.7z.002`, fsys, protect.List{}, sysEnv)
	if err != nil {
		t.Fatal(err)
	}
	if set.First != `D:\dl\set.7z.001` || set.Picked != `D:\dl\set.7z.002` || len(set.Parts) != 3 || set.Bytes != 3_000 || set.Format != archive.Format7z {
		t.Fatalf("set = %+v", set)
	}
	fsys.AddFile(`D:\dl\only.7z.002`, 10)
	if _, err := extract.Identify(`D:\dl\only.7z.002`, fsys, protect.List{}, sysEnv); !errors.Is(err, extract.ErrNotAFile) {
		t.Errorf("missing first part: %v", err)
	}
	fsys.AddFile(`D:\dl\x.z01`, 10)
	if _, err := extract.Identify(`D:\dl\x.z01`, fsys, protect.List{}, sysEnv); !errors.Is(err, archive.ErrSplitZip) {
		t.Errorf("split zip: %v", err)
	}
	fsys.AddFile(`D:\dl\notes.txt`, 10)
	if _, err := extract.Identify(`D:\dl\notes.txt`, fsys, protect.List{}, sysEnv); !errors.Is(err, archive.ErrNotArchive) {
		t.Errorf("not an archive: %v", err)
	}
	// A path with nothing at it is "not there", told apart from a folder.
	for _, missing := range []string{`D:\dl\missing.zip`, `D:\nowhere\missing.zip`, `D:\dl\missing.7z.001`} {
		if _, err := extract.Identify(missing, fsys, protect.List{}, sysEnv); !errors.Is(err, fs.ErrNotExist) || !errors.Is(err, extract.ErrNotAFile) {
			t.Errorf("missing %s: %v", missing, err)
		}
	}
	fsys.AddDir(`D:\dl\folder.zip`)
	if _, err := extract.Identify(`D:\dl\folder.zip`, fsys, protect.List{}, sysEnv); !errors.Is(err, extract.ErrNotAFile) || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a folder: %v", err)
	}
	if _, err := extract.Identify(`D:\dl\only.7z.002`, fsys, protect.List{}, sysEnv); !errors.Is(err, extract.ErrFirstPartMissing) || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a part without the first part: %v", err)
	}
	if _, err := extract.Identify(`relative\a.zip`, fsys, protect.List{}, sysEnv); err == nil {
		t.Error("a relative path was accepted")
	}
	if set, err := extract.Identify(`  "D:\dl\set.7z.003"  `, fsys, protect.List{}, sysEnv); err != nil || set.First != `D:\dl\set.7z.001` {
		t.Errorf("a quoted path: %+v %v", set, err)
	}
}

// Safety rule 82: links are refused, the file itself or a folder above it.
func TestIdentifyRefusesLinksInTheFakeFS(t *testing.T) {
	fsys := extracttest.NewFakeFS()
	fsys.AddLink(`D:\dl\link.zip`)
	if _, err := extract.Identify(`D:\dl\link.zip`, fsys, protect.List{}, sysEnv); !errors.Is(err, extract.ErrReparsePoint) {
		t.Errorf("a linked file: %v", err)
	}
	fsys.AddLink(`D:\junction`)
	fsys.AddFile(`D:\junction\inner.zip`, 10)
	if _, err := extract.Identify(`D:\junction\inner.zip`, fsys, protect.List{}, sysEnv); !errors.Is(err, extract.ErrReparsePoint) {
		t.Errorf("through a junction: %v", err)
	}
}

// Safety rule 82: every part is validated, not only the picked one.
func TestAllPartsAreValidated(t *testing.T) {
	fsys := extracttest.NewFakeFS()
	fsys.AddFile(`D:\dl\v.part1.rar`, 10)
	fsys.AddLink(`D:\dl\v.part2.rar`)
	set := archive.Set{Picked: `D:\dl\v.part1.rar`, First: `D:\dl\v.part1.rar`, Parts: []string{`D:\dl\v.part1.rar`, `D:\dl\v.part2.rar`}, Format: archive.FormatRar}
	if err := extract.Validate(set, fsys, protect.List{}, sysEnv); !errors.Is(err, extract.ErrReparsePoint) {
		t.Errorf("a linked part: %v", err)
	}
	fsys.AddFile(`C:\Windows\v.part2.rar`, 10)
	set.Parts[1] = `C:\Windows\v.part2.rar`
	if err := extract.Validate(set, fsys, protect.List{}, sysEnv); !errors.Is(err, extract.ErrProtected) {
		t.Errorf("a part in Windows: %v", err)
	}
	set.Parts = set.Parts[:1]
	if err := extract.Validate(set, fsys, protect.List{}, sysEnv); err != nil {
		t.Errorf("one good part: %v", err)
	}
	if err := extract.Validate(archive.Set{}, fsys, protect.List{}, sysEnv); err == nil {
		t.Error("an empty set validated")
	}
}

// Safety rule 82 and decision 11: the output folder is <stem> beside the
// archive, for single files and every split naming, never a drive root.
func TestOutDirIsANewFolderBesideTheArchive(t *testing.T) {
	fsys := extracttest.NewFakeFS()
	for first, want := range map[string]string{
		`D:\Downloads\Game-Backup.zip`:  `D:\Downloads\Game-Backup`,
		`D:\Downloads\data.7z.001`:      `D:\Downloads\data`,
		`D:\Downloads\movie.part01.rar`: `D:\Downloads\movie`,
		`D:\Downloads\old.rar`:          `D:\Downloads\old`,
		`D:\top.zip`:                    `D:\top`,
	} {
		set := archive.Set{First: first, Parts: []string{first}}
		if got := extract.DefaultOutDir(set, fsys); got != want {
			t.Errorf("DefaultOutDir(%s) = %s, want %s", first, got, want)
		}
		fsys.AddFile(first, 10)
		if err := extract.Validate(archive.Set{Picked: first, First: first, Parts: []string{first}}, fsys, protect.List{}, sysEnv); err != nil {
			t.Errorf("Validate(%s) = %v", first, err)
		}
	}
}

// Decision 11: a fresh run never writes into someone's non-empty folder.
func TestAnExistingFolderGetsANumber(t *testing.T) {
	fsys := extracttest.NewFakeFS()
	set := archive.Set{First: `D:\dl\x.zip`, Parts: []string{`D:\dl\x.zip`}}
	fsys.AddDir(`D:\dl\x`)
	if got := extract.DefaultOutDir(set, fsys); got != `D:\dl\x` {
		t.Errorf("empty folder: %s", got)
	}
	fsys.AddFile(`D:\dl\x\mine.txt`, 1)
	if got := extract.DefaultOutDir(set, fsys); got != `D:\dl\x (2)` {
		t.Errorf("non-empty: %s", got)
	}
	fsys.AddFile(`D:\dl\x (2)\more.txt`, 1)
	if got := extract.DefaultOutDir(set, fsys); got != `D:\dl\x (3)` {
		t.Errorf("two taken: %s", got)
	}
	fsys.AddFile(`D:\dl\y`, 1) // a file with the folder's name
	if got := extract.DefaultOutDir(archive.Set{First: `D:\dl\y.zip`}, fsys); got != `D:\dl\y (2)` {
		t.Errorf("a file in the way: %s", got)
	}
	fsys.AddFile(archive.JournalPath(`D:\dl\x.zip`), 10) // resuming: the same folder
	if got := extract.DefaultOutDir(set, fsys); got != `D:\dl\x` {
		t.Errorf("with a journal: %s", got)
	}
}

// --- FindArchives ---

func TestFindArchivesGroupsPartsAndSumsBytes(t *testing.T) {
	fsys := extracttest.NewFakeFS()
	fsys.AddFile(`D:\dl\big.zip`, 3_000_000_000)
	fsys.AddFile(`D:\dl\small.zip`, 10_000)
	for _, p := range []string{`D:\dl\set.7z.001`, `D:\dl\set.7z.002`, `D:\dl\set.7z.003`} {
		fsys.AddFile(p, 400_000_000)
	}
	for _, p := range []string{`D:\dl\m.part1.rar`, `D:\dl\m.part2.rar`} {
		fsys.AddFile(p, 600_000_000)
	}
	fsys.AddFile(`D:\dl\orphan.7z.002`, 5_000_000_000) // no first part
	fsys.AddFile(`D:\dl\x.z01`, 5_000_000_000)
	fsys.AddFile(`D:\dl\big.zip.hatchzip-part`, 5_000_000_000)
	fsys.AddFile(`D:\dl\deep\inner.zip`, 2_000_000_000)
	got := extract.FindArchives(context.Background(), fsys, []string{`D:\dl`, `D:\dl`}, extract.OfferMinBytes, 1, 1000)
	byFirst := map[string]extract.Found{}
	for _, f := range got {
		byFirst[f.Set.First] = f
	}
	if len(got) != 3 {
		t.Fatalf("found %d: %+v", len(got), got)
	}
	if s := byFirst[`D:\dl\set.7z.001`].Set; len(s.Parts) != 3 || s.Bytes != 1_200_000_000 {
		t.Errorf("7z set = %+v", s)
	}
	if s := byFirst[`D:\dl\m.part1.rar`].Set; len(s.Parts) != 2 || s.Bytes != 1_200_000_000 {
		t.Errorf("rar set = %+v", s)
	}
	if _, ok := byFirst[`D:\dl\big.zip`]; !ok {
		t.Error("big.zip missing")
	}
	// depth 2 also finds the one in a subfolder.
	if got := extract.FindArchives(context.Background(), fsys, []string{`D:\dl`}, extract.OfferMinBytes, 2, 1000); len(got) != 4 {
		t.Errorf("depth 2 found %d", len(got))
	}
}

func TestFindArchivesStopsAtMaxEntriesAndOnCancel(t *testing.T) {
	fsys := extracttest.NewFakeFS()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		fsys.AddFile(`D:\dl\`+n+`.zip`, 2_000_000_000)
	}
	if got := extract.FindArchives(context.Background(), fsys, []string{`D:\dl`}, 1, 1, 2); len(got) != 2 {
		t.Errorf("max 2 entries found %d", len(got))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := extract.FindArchives(ctx, fsys, []string{`D:\dl`}, 1, 1, 100); len(got) != 0 {
		t.Errorf("cancelled search found %d", len(got))
	}
}

// An unfinished set is listed whatever its size (in place it has shrunk),
// and marked.
func TestFindArchivesMarksUnfinished(t *testing.T) {
	fsys := extracttest.NewFakeFS()
	fsys.AddFile(`D:\dl\half.zip`, 50_000)
	fsys.AddFile(archive.JournalPath(`D:\dl\half.zip`), 500)
	fsys.AddFile(`D:\dl\new.zip`, 2_000_000_000)
	got := extract.FindArchives(context.Background(), fsys, []string{`D:\dl`}, extract.OfferMinBytes, 1, 100)
	if len(got) != 2 {
		t.Fatalf("found %+v", got)
	}
	for _, f := range got {
		if f.Unfinished != (f.Set.First == `D:\dl\half.zip`) {
			t.Errorf("%s unfinished = %v", f.Set.First, f.Unfinished)
		}
	}
}

func TestFindArchivesNeverEntersLinks(t *testing.T) {
	fsys := extracttest.NewFakeFS()
	fsys.AddLink(`D:\dl\linked`)
	fsys.AddFile(`D:\dl\linked\inside.zip`, 2_000_000_000)
	fsys.AddLink(`D:\dl\link.zip`)
	fsys.AddFile(`D:\dl\real\a.zip`, 2_000_000_000)
	got := extract.FindArchives(context.Background(), fsys, []string{`D:\dl`}, 1, 3, 100)
	if len(got) != 1 || got[0].Set.First != `D:\dl\real\a.zip` {
		t.Fatalf("found %+v", got)
	}
	for _, c := range fsys.Calls() {
		if c == `readdir D:\dl\linked` {
			t.Fatal("entered a link")
		}
	}
}
