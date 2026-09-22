package wt

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
)

const fallback = "Symbols Nerd Font Mono"

// copyFixture copies a testdata fixture into a fresh temp dir so tests
// never mutate the checked-in fixtures and never interfere with each
// other.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	dst := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write fixture copy: %v", err)
	}
	return dst
}

func TestPatch_CommentsAndTrailingCommasSurvive(t *testing.T) {
	path := copyFixture(t, "comments_and_trailing_commas.json")

	res, err := Patch(path, fallback)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true")
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)

	for _, want := range []string{
		"// Windows Terminal settings",
		"// default font settings",
		"// trailing comment",
		`"Cascadia Mono, Symbols Nerd Font Mono"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q\n---\n%s", want, s)
		}
	}

	// The trailing-comma input must not have broken parsing (it parsed,
	// since Patch got this far); re-parsing the output must also succeed.
	assertParsesAsFace(t, path, "profiles/defaults/font/face", "Cascadia Mono, Symbols Nerd Font Mono")
}

func TestPatch_DefaultsWithoutFont(t *testing.T) {
	path := copyFixture(t, "defaults_without_font.json")

	res, err := Patch(path, fallback)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true")
	}
	assertParsesAsFace(t, path, "profiles/defaults/font/face", "Cascadia Mono, Symbols Nerd Font Mono")
}

// TestPatchPreservesABOM covers finding 9: a settings.json saved by an
// editor that writes a UTF-8 byte-order mark (Notepad does; VS Code and
// Terminal's own settings UI do not) must still parse, and the BOM must
// survive round-tripping: present on the patched file, present on the
// backup (which keeps the original bytes byte-for-byte), absent from
// nowhere it wasn't already.
func TestPatchPreservesABOM(t *testing.T) {
	path := copyFixture(t, "bom.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if !strings.HasPrefix(string(original), "\xEF\xBB\xBF") {
		t.Fatalf("fixture bom.json does not start with a BOM")
	}

	res, err := Patch(path, fallback)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true")
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("\xEF\xBB\xBF")) {
		t.Fatalf("patched file lost its BOM: %q", out[:min(16, len(out))])
	}
	stripped := out[len("\xEF\xBB\xBF"):]
	root := mustParse(t, stripped)
	v := root.Find("/profiles/defaults/font/face")
	if v == nil {
		t.Fatalf("pointer /profiles/defaults/font/face not found in:\n%s", stripped)
	}
	lit, ok := v.Value.(hujson.Literal)
	if !ok {
		t.Fatalf("pointer is not a literal: %#v", v.Value)
	}
	if got, want := lit.String(), "Cascadia Mono, Symbols Nerd Font Mono"; got != want {
		t.Fatalf("face = %q, want %q", got, want)
	}

	backup, err := os.ReadFile(path + backupSuffix)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !bytes.Equal(backup, original) {
		t.Fatalf("backup does not match the original fixture bytes (BOM and all)\ngot:  %q\nwant: %q", backup, original)
	}
}

func TestPatch_DefaultsWithFontFace(t *testing.T) {
	path := copyFixture(t, "defaults_with_font_face.json")

	res, err := Patch(path, fallback)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true")
	}
	assertParsesAsFace(t, path, "profiles/defaults/font/face", "Consolas, Symbols Nerd Font Mono")

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"size"`) {
		t.Error("expected unrelated \"size\" key to survive")
	}
}

func TestPatch_ProfileOverride(t *testing.T) {
	path := copyFixture(t, "profile_with_override.json")

	res, err := Patch(path, fallback)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true")
	}

	assertParsesAsFace(t, path, "profiles/defaults/font/face", "Cascadia Mono, Symbols Nerd Font Mono")
	assertParsesAsFace(t, path, "profiles/list/0/font/face", "Ubuntu Mono, Symbols Nerd Font Mono")

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"CMD"`) {
		// CMD profile (list/1) must not have gained a font override.
		root := mustParse(t, out)
		if v := root.Find("/profiles/list/1/font"); v != nil {
			t.Error("expected the CMD profile (no prior font override) to stay untouched")
		}
	}
}

func TestPatch_AlreadyPatchedIsIdempotent(t *testing.T) {
	path := copyFixture(t, "already_patched.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	res, err := Patch(path, fallback)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if res.Changed {
		t.Fatal("expected Changed=false: fallback is already present everywhere")
	}
	if res.BackupPath != "" {
		t.Fatalf("expected no backup to be created, got %q", res.BackupPath)
	}
	if _, statErr := os.Stat(path + backupSuffix); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("expected no backup file on disk")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("file was rewritten despite being a no-op change\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// The fallback must not have been appended twice.
	assertParsesAsFace(t, path, "profiles/defaults/font/face", "Cascadia Mono, Symbols Nerd Font Mono")
}

func TestPatch_LegacyFontFace(t *testing.T) {
	path := copyFixture(t, "legacy_fontface.json")

	res, err := Patch(path, fallback)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !res.Changed {
		t.Fatal("expected Changed=true")
	}

	assertParsesAsFace(t, path, "profiles/defaults/font/face", "Consolas, Symbols Nerd Font Mono")
	assertParsesAsFace(t, path, "profiles/list/0/font/face", "Ubuntu Mono, Symbols Nerd Font Mono")

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "fontFace") {
		t.Error("expected legacy \"fontFace\" key to be migrated away")
	}
}

func TestPatch_GarbageIsUntouched(t *testing.T) {
	path := copyFixture(t, "garbage.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Patch(path, fallback)
	if err == nil {
		t.Fatal("expected an error for unparsable input")
	}
	var unparsable *UnparsableError
	if !errors.As(err, &unparsable) {
		t.Fatalf("expected *UnparsableError, got %T: %v", err, err)
	}
	if unparsable.Path != path {
		t.Errorf("UnparsableError.Path = %q, want %q", unparsable.Path, path)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("garbage file was modified despite being unparsable")
	}
	if _, err := os.Stat(path + backupSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expected no backup to be created for an unparsable file")
	}
}

func TestPatch_BackupNotClobberedOnSecondPatch(t *testing.T) {
	path := copyFixture(t, "defaults_with_font_face.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, patchErr := Patch(path, fallback); patchErr != nil {
		t.Fatalf("first Patch: %v", patchErr)
	}
	backupPath := path + backupSuffix
	firstBackup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(firstBackup) != string(original) {
		t.Fatal("backup does not match the pristine original")
	}

	// Simulate the user (or Windows Terminal) adding a new profile with
	// its own font override after Devpit's first patch, forcing a second,
	// real change.
	patched, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	withNewProfile := strings.Replace(string(patched),
		`"list": [`,
		`"list": [
            { "guid": "{cccc}", "name": "New", "font": { "face": "Iosevka" } },`,
		1)
	if writeErr := os.WriteFile(path, []byte(withNewProfile), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}

	if _, patchErr := Patch(path, fallback); patchErr != nil {
		t.Fatalf("second Patch: %v", patchErr)
	}
	secondBackup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup after second patch: %v", err)
	}
	if string(secondBackup) != string(firstBackup) {
		t.Fatal("backup was overwritten on the second patch")
	}

	assertParsesAsFace(t, path, "profiles/list/0/font/face", "Iosevka, Symbols Nerd Font Mono")
}

func TestRestore_RoundTrip(t *testing.T) {
	path := copyFixture(t, "defaults_with_font_face.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, patchErr := Patch(path, fallback); patchErr != nil {
		t.Fatalf("Patch: %v", patchErr)
	}
	patched, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(patched) == string(original) {
		t.Fatal("Patch should have changed the file")
	}

	if restoreErr := Restore(path); restoreErr != nil {
		t.Fatalf("Restore: %v", restoreErr)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(original) {
		t.Fatalf("restored content does not match original\ngot:\n%s\nwant:\n%s", restored, original)
	}
}

func TestManualSnippet(t *testing.T) {
	snippet := ManualSnippet(fallback)
	if !strings.Contains(snippet, fallback) {
		t.Fatalf("ManualSnippet does not mention %q: %s", fallback, snippet)
	}
	if !strings.Contains(snippet, "font") || !strings.Contains(snippet, "face") {
		t.Fatalf("ManualSnippet does not look like a font.face snippet: %s", snippet)
	}
}

func TestFindSettings(t *testing.T) {
	base := t.TempDir()

	stablePath := filepath.Join(base, "Packages", "Microsoft.WindowsTerminal_8wekyb3d8bbwe", "LocalState", "settings.json")
	unpackagedPath := filepath.Join(base, "Microsoft", "Windows Terminal", "settings.json")

	for _, p := range []string{stablePath, unpackagedPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Preview is deliberately absent to prove FindSettings only returns
	// what actually exists.

	got := FindSettings(FindOptions{LocalAppData: base})
	if len(got) != 2 {
		t.Fatalf("got %d locations, want 2: %+v", len(got), got)
	}
	byFlavor := map[string]Location{}
	for _, loc := range got {
		byFlavor[loc.Flavor] = loc
	}
	if byFlavor[FlavorStable].Path != stablePath {
		t.Errorf("stable path = %q, want %q", byFlavor[FlavorStable].Path, stablePath)
	}
	if byFlavor[FlavorUnpackaged].Path != unpackagedPath {
		t.Errorf("unpackaged path = %q, want %q", byFlavor[FlavorUnpackaged].Path, unpackagedPath)
	}
	if _, ok := byFlavor[FlavorPreview]; ok {
		t.Error("did not expect a preview location: it was never created")
	}
}

func TestFindSettings_None(t *testing.T) {
	got := FindSettings(FindOptions{LocalAppData: t.TempDir()})
	if len(got) != 0 {
		t.Fatalf("got %d locations, want 0: %+v", len(got), got)
	}
}

// mustParse parses data as HuJSON, failing the test on error.
func mustParse(t *testing.T, data []byte) hujson.Value {
	t.Helper()
	v, err := hujson.Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return v
}

// assertParsesAsFace re-reads path, parses it as HuJSON and asserts that
// the string value at the given JSON pointer equals want. It fails loudly
// (rather than skipping) when the pointer resolves to nothing or to a
// non-string, since that means Patch produced structurally wrong output.
func assertParsesAsFace(t *testing.T, path, pointer, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	root := mustParse(t, data)
	v := root.Find("/" + pointer)
	if v == nil {
		t.Fatalf("pointer /%s not found in:\n%s", pointer, data)
	}
	lit, ok := v.Value.(hujson.Literal)
	if !ok {
		t.Fatalf("pointer /%s is not a literal: %#v", pointer, v.Value)
	}
	if got := lit.String(); got != want {
		t.Fatalf("pointer /%s = %q, want %q", pointer, got, want)
	}
}
