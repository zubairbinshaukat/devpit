package extract

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Safety rule 78: no non-test source of the engine opens or reads a file.
// The one allowed call is fs.go's directory handle for folder names (spike
// X1); record.go writes its own file through share/store, never os.*, and
// logfile.go creates the run log, a new file in Devpit's log folder (rule 91).
func TestEngineSourceNeverOpensFiles(t *testing.T) {
	banned := regexp.MustCompile(`os\.(Open|OpenFile|ReadFile|Create|CreateTemp|ReadDir|DirFS)\(|\.Info\(\)|ioutil\.|filepath\.(Walk|WalkDir)\(|fs\.(WalkDir|ReadFile|ReadDir)\(`)
	allowed := map[string]int{
		"fs.go":      1, // osFS.ReadDirNames: os.Open(dir), checked to be a directory
		"logfile.go": 1, // openLogFile: os.OpenFile with O_EXCL, a new log in the log folder
	}
	for _, f := range sourceFiles(t) {
		n := len(banned.FindAllString(stripComments(readSource(t, f)), -1))
		if n != allowed[f] {
			t.Errorf("%s: %d file-opening calls, want %d", f, n, allowed[f])
		}
	}
	if !strings.Contains(readSource(t, "fs.go"), "if !fi.IsDir()") {
		t.Error("fs.go must refuse to open anything but a directory")
	}
	lf := stripComments(readSource(t, "logfile.go"))
	if !strings.Contains(lf, "os.O_WRONLY|os.O_CREATE|os.O_EXCL") || strings.Contains(lf, "O_RDONLY") || strings.Contains(lf, "O_RDWR") {
		t.Error("logfile.go must only ever create a new file, write-only")
	}
}

// Safety rule 79: the engine never changes its own environment.
func TestNoSetenvInTheEngine(t *testing.T) {
	if hits := sourceMatches(t, regexp.MustCompile(`os\.(Setenv|Unsetenv|Clearenv)\(|syscall\.Setenv\(`)); len(hits) > 0 {
		t.Fatalf("environment changed in %v", hits)
	}
}

// Safety rules 81 and 91: no source removes, renames or edits a file, so
// hatchzip's side files and the archive are never touched. The record file goes
// through share/store (record.go); the run logs are created and rotated by
// logfile.go, only by names matching logName inside the log folder.
func TestEngineNeverRemovesOrRenamesAnythingButItsRecord(t *testing.T) {
	banned := regexp.MustCompile(`os\.(Remove|RemoveAll|Rename|Truncate|WriteFile|Chmod|Chtimes|Link|Symlink|Mkdir|MkdirAll)\(|store\.(Remove|WriteJSON)\(`)
	hits := sourceMatches(t, banned)
	if !slices.Equal(hits, []string{"logfile.go", "record.go"}) {
		t.Fatalf("file-changing calls in %v, want only logfile.go and record.go", hits)
	}
	lf := stripComments(readSource(t, "logfile.go"))
	for _, call := range regexp.MustCompile(`os\.(Remove|RemoveAll|Rename|Truncate|WriteFile|Chmod|Chtimes|Link|Symlink|Mkdir|MkdirAll)\(([^)]*)\)`).FindAllStringSubmatch(lf, -1) {
		switch {
		case call[1] == "MkdirAll" && strings.HasPrefix(call[2], "dir,"):
		case call[1] == "Remove" && call[2] == "filepath.Join(dir, name":
		default:
			t.Errorf("logfile.go: os.%s(%s), want only MkdirAll(dir) and Remove(filepath.Join(dir, name))", call[1], call[2])
		}
	}
	// rotateLogs removes only names that match the log pattern.
	if i := strings.Index(lf, "func rotateLogs"); i < 0 || !strings.Contains(lf[i:], "if !logName.MatchString(name)") {
		t.Error("rotateLogs must skip names that are not run logs")
	}
	rec := stripComments(readSource(t, "record.go"))
	// record.go only ever writes or removes the path of its own file.
	for _, call := range regexp.MustCompile(`store\.(Remove|WriteJSON)\(([^,)]*)`).FindAllStringSubmatch(rec, -1) {
		if strings.TrimSpace(call[2]) != "p" {
			t.Errorf("record.go: store.%s(%s), want only its own path p", call[1], call[2])
		}
	}
	if !strings.Contains(rec, "filepath.Join(d, recordFile)") {
		t.Error("record.go's path is not its own record file")
	}
	// The StatFS the engine is given has no method that changes anything.
	api := readSource(t, "api.go")
	start := strings.Index(api, "type StatFS interface {")
	end := strings.Index(api[start:], "}")
	iface := api[start : start+end]
	for _, m := range []string{"Remove", "Rename", "Open", "Create", "Write", "Truncate"} {
		if strings.Contains(iface, m) {
			t.Errorf("StatFS has %s", m)
		}
	}
}
