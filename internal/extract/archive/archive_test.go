package archive

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestClassifySupportedNames(t *testing.T) {
	cases := []struct {
		name  string
		f     Format
		first string
	}{
		{"x.zip", FormatZip, "x.zip"},
		{"x.7z", Format7z, "x.7z"},
		{"x.rar", FormatRar, "x.rar"},
		{"x.7z.001", Format7z, "x.7z.001"},
		{"x.7z.003", Format7z, "x.7z.001"},
		{"x.zip.001", FormatZip, "x.zip.001"},
		{"x.zip.0002", FormatZip, "x.zip.0001"},
		{"x.part1.rar", FormatRar, "x.part1.rar"},
		{"x.part3.rar", FormatRar, "x.part1.rar"},
		{"x.part01.rar", FormatRar, "x.part01.rar"},
		{"x.part12.rar", FormatRar, "x.part01.rar"},
		{"x.r00", FormatRar, "x.rar"},
		{"x.r05", FormatRar, "x.rar"},
		{"GAME.ZIP", FormatZip, "GAME.ZIP"},
		{"Data.7Z.002", Format7z, "Data.7Z.001"},
		{"BIG.PART2.RAR", FormatRar, "BIG.PART1.RAR"},
		{"BIG.R01", FormatRar, "BIG.RAR"},
		{"my.backup.2026.zip", FormatZip, "my.backup.2026.zip"},
		{`D:\Downloads\x.7z.004`, Format7z, `D:\Downloads\x.7z.001`},
		{"/home/u/x.part2.rar", FormatRar, "/home/u/x.part1.rar"},
	}
	for _, c := range cases {
		f, first, ok, err := Classify(c.name)
		if !ok || err != nil || f != c.f || first != c.first {
			t.Errorf("Classify(%q) = %q %q %v %v, want %q %q", c.name, f, first, ok, err, c.f, c.first)
		}
		if !IsSupported(c.name) {
			t.Errorf("IsSupported(%q) = false", c.name)
		}
	}
}

func TestClassifyRefusesOtherNames(t *testing.T) {
	for _, name := range []string{
		"x", "x.", ".zip", "x.txt", "x.tar.gz", "x.tgz", "x.001", "x.7z.01",
		"x.zip.hatchzip", "x.hatchzip-part", "x.iso", "x.exe",
	} {
		_, _, ok, err := Classify(name)
		if ok || !errors.Is(err, ErrNotArchive) {
			t.Errorf("Classify(%q) = %v %v, want ErrNotArchive", name, ok, err)
		}
		if IsSupported(name) {
			t.Errorf("IsSupported(%q) = true", name)
		}
	}
}

func TestSplitZipIsRefused(t *testing.T) {
	for _, name := range []string{"x.z01", "x.z02", "X.Z10", "x.z001"} {
		_, _, ok, err := Classify(name)
		if ok || !errors.Is(err, ErrSplitZip) {
			t.Errorf("Classify(%q) = %v %v, want ErrSplitZip", name, ok, err)
		}
	}
}

func TestFirstVolume(t *testing.T) {
	cases := map[string]string{
		"x.7z.003":              "x.7z.001",
		"x.part3.rar":           "x.part1.rar",
		"x.part003.rar":         "x.part001.rar",
		"x.r05":                 "x.rar",
		"x.zip":                 "x.zip",
		`C:\a\b.zip.010`:        `C:\a\b.zip.001`,
		"notes.txt":             "notes.txt",
		"x.z01":                 "x.z01",
		"D:/Downloads/y.r00":    "D:/Downloads/y.rar",
		"D:/Downloads/y.R00":    "D:/Downloads/y.RAR",
		"dataset.7z.001":        "dataset.7z.001",
		"Game-Backup.part1.rar": "Game-Backup.part1.rar",
	}
	for in, want := range cases {
		if got := FirstVolume(in); got != want {
			t.Errorf("FirstVolume(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVolume(t *testing.T) {
	cases := []struct {
		path string
		n    int
		want string
		ok   bool
	}{
		{"x.7z.001", 3, "x.7z.003", true},
		{"x.7z.005", 1, "x.7z.001", true},
		{"x.part01.rar", 10, "x.part10.rar", true},
		{"x.rar", 1, "x.rar", true},
		{"x.rar", 2, "x.r00", true},
		{"x.rar", 12, "x.r10", true},
		{"x.r03", 1, "x.rar", true},
		{"x.zip", 1, "x.zip", true},
		{"x.zip", 2, "", false},
		{"x.7z", 2, "", false},
		{"x.txt", 1, "", false},
		{"x.zip", 0, "", false},
		{`D:\d\x.7z.001`, 2, `D:\d\x.7z.002`, true},
	}
	for _, c := range cases {
		got, ok := Volume(c.path, c.n)
		if got != c.want || ok != c.ok {
			t.Errorf("Volume(%q, %d) = %q %v, want %q %v", c.path, c.n, got, ok, c.want, c.ok)
		}
	}
}

func TestStem(t *testing.T) {
	cases := map[string]string{
		"x.zip":                        "x",
		"x.7z.001":                     "x",
		"x.part01.rar":                 "x",
		"x.part1.rar":                  "x",
		"x.rar":                        "x",
		"x.zip.001":                    "x",
		`D:\Downloads\Game-Backup.zip`: "Game-Backup",
		"my.backup.2026.7z":            "my.backup.2026",
		"notes.txt":                    "notes",
	}
	for in, want := range cases {
		if got := Stem(in); got != want {
			t.Errorf("Stem(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSideFileNames(t *testing.T) {
	if got := JournalPath(`D:\x.7z.001`); got != `D:\x.7z.001.hatchzip` {
		t.Fatalf("JournalPath = %q", got)
	}
	for _, name := range []string{
		"x.zip.hatchzip", "x.zip.hatchzip-tail", "x.zip.hatchzip-folders",
		"file.bin.hatchzip-part", "X.ZIP.HATCHZIP",
	} {
		if !IsSideFile(name) {
			t.Errorf("IsSideFile(%q) = false", name)
		}
	}
	for _, name := range []string{"x.zip", "hatchzip.exe", "x.hatchzip-partial", "x.hatchzipx"} {
		if IsSideFile(name) {
			t.Errorf("IsSideFile(%q) = true", name)
		}
	}
}

// TestArchiveIsALeafPackage pins that archive imports only the standard
// library, so any package can import it without pulling the engine in.
func TestArchiveIsALeafPackage(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	for _, goos := range []string{"windows", "linux"} {
		cmd := exec.Command(goBin, "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".")
		cmd.Env = append(cmd.Environ(), "GOOS="+goos)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, dep := range strings.Fields(string(out)) {
			if !strings.HasSuffix(dep, "/internal/extract/archive") {
				t.Errorf("%s: archive imports %s", goos, dep)
			}
		}
	}
}
