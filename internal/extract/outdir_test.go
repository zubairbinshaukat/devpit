package extract_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/extracttest"
)

// Choosing where to extract (rule 82): OutDirIn makes a new <stem> folder in
// the chosen parent, or refuses the parent.

// Safety rule 82: a chosen parent gets a new <stem> folder, numbered when
// that exists and is not empty, like the folder beside the archive.
func TestOutDirInIsANewFolderInTheChosenParent(t *testing.T) {
	set := request().Set
	fsys := extracttest.NewFakeFS()
	fsys.AddFile(zipPath, 6_000)
	fsys.AddDir(`E:\Out`)
	got, err := extract.OutDirIn(`E:\Out`, set, fsys)
	if err != nil || got != `E:\Out\Game-Backup` {
		t.Fatalf("fresh = %q, %v", got, err)
	}
	if got, _ := extract.OutDirIn(`  "E:\Out\"  `, set, fsys); got != `E:\Out\Game-Backup` {
		t.Fatalf("quoted with a trailing slash = %q", got)
	}
	fsys.AddDir(`E:\Out\Game-Backup`) // empty: used as is
	if got, _ := extract.OutDirIn(`E:\Out`, set, fsys); got != `E:\Out\Game-Backup` {
		t.Fatalf("empty folder = %q", got)
	}
	fsys.AddFile(`E:\Out\Game-Backup\someone.txt`, 1)
	if got, _ := extract.OutDirIn(`E:\Out`, set, fsys); got != `E:\Out\Game-Backup (2)` {
		t.Fatalf("non-empty = %q", got)
	}
	fsys.AddFile(`E:\Out\Game-Backup (2)`, 1) // a file of that name
	if got, _ := extract.OutDirIn(`E:\Out`, set, fsys); got != `E:\Out\Game-Backup (3)` {
		t.Fatalf("taken by a file = %q", got)
	}
	// A drive root is a fine parent; the output is never the root itself.
	if got, err := extract.OutDirIn(`E:\`, set, fsys); err != nil || got != `E:\Game-Backup` {
		t.Fatalf("root = %q, %v", got, err)
	}
	// A split set is named after its stem.
	split := set
	split.First, split.Picked = `D:\dl\set.7z.001`, `D:\dl\set.7z.002`
	if got, _ := extract.OutDirIn(`E:\Out`, split, fsys); got != `E:\Out\set` {
		t.Fatalf("split = %q", got)
	}
}

// Safety rule 82: a missing or non-folder parent, a network path, a link or
// a junction (itself or above), a system folder, and a relative path are all
// refused with the exported errors.
func TestOutDirInRefusals(t *testing.T) {
	t.Setenv("ProgramData", `C:\ProgramData`)
	t.Setenv("WINDIR", `C:\Windows`)
	set := request().Set
	fsys := extracttest.NewFakeFS()
	fsys.AddFile(zipPath, 6_000)
	fsys.AddFile(`E:\file.txt`, 1)
	fsys.AddLink(`E:\link`)
	fsys.AddLink(`E:\junction`)
	fsys.AddDir(`E:\junction\inner`)
	fsys.AddDir(`C:\ProgramData\x`)
	fsys.AddDir(`C:\Windows\Temp`)
	for parent, want := range map[string]error{
		`E:\missing`:             extract.ErrNotAFolder,
		`E:\file.txt`:            extract.ErrNotAFolder,
		`relative\dir`:           extract.ErrNotAFolder,
		``:                       extract.ErrNotAFolder,
		`\\server\share\x`:       extract.ErrNetworkPath,
		`//server/share/x`:       extract.ErrNetworkPath,
		`\\?\UNC\server\share\x`: extract.ErrNetworkPath,
		`E:\link`:                extract.ErrReparsePoint,
		`E:\junction\inner`:      extract.ErrReparsePoint,
		`C:\ProgramData\x`:       extract.ErrProtected,
		`C:\Windows\Temp`:        extract.ErrProtected,
		`c:\windows\temp`:        extract.ErrProtected,
	} {
		got, err := extract.OutDirIn(parent, set, fsys)
		if !errors.Is(err, want) || got != "" {
			t.Errorf("%q: %q, %v; want %v", parent, got, err, want)
		}
	}
	if _, err := extract.OutDirIn(`E:\Out`, set, nil); err == nil {
		t.Error("no file system accepted")
	}
}

// Safety rules 82 and 83: the engine accepts a folder from OutDirIn on
// another drive, and hatchzip is given exactly that folder.
func TestTheEngineAcceptsAChosenParentOnAnotherDrive(t *testing.T) {
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Exit: 0})
	e.f.FS.AddDir(`E:\Out`)
	r := request()
	out, err := extract.OutDirIn(`E:\Out`, r.Set, e.f.FS)
	if err != nil {
		t.Fatal(err)
	}
	r.OutDir = out
	o := e.wait(e.start(context.Background(), r))
	if o.Kind != extract.KindDone {
		t.Fatalf("outcome = %+v", o)
	}
	specs := e.extractSpecs()
	if len(specs) != 1 || specs[0].Args[3] != `E:\Out\Game-Backup` {
		t.Fatalf("specs = %+v", specs)
	}
	if extract.SameDrive(r.Set.First, r.OutDir) {
		t.Fatal("D: and E: are the same drive")
	}
}

func TestSameDrive(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{`D:\a\x.zip`, `D:\b`, true},
		{`d:\a`, `D:/b`, true},
		{`D:\a`, `E:\a`, false},
		{`\\?\D:\a`, `D:\b`, true},
		{`\\srv\share\a`, `\\SRV\Share\b`, true},
		{`\\srv\share\a`, `\\srv\data\a`, false},
		{`\\srv\share\a`, `D:\a`, false},
		{`/tmp/a`, `/home/b`, true},
	} {
		if got := extract.SameDrive(c.a, c.b); got != c.want {
			t.Errorf("SameDrive(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
