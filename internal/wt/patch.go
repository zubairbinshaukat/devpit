package wt

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tailscale/hujson"
)

// backupSuffix is appended to the settings.json path to form its backup
// path. Patch never overwrites an existing backup: the first backup taken
// is assumed to be the user's pristine, pre-Devpit file.
const backupSuffix = ".devpit-bak"

// utf8BOM is the byte-order mark some editors (notably Windows Notepad)
// write at the start of a UTF-8 file. VS Code and the Terminal's own
// settings UI never add one, but a user who has hand-edited settings.json
// in Notepad can have it. hujson.Parse has no idea what to do with those
// three bytes and fails the whole parse over them, so Patch strips a
// leading BOM before parsing and re-adds it when writing, both to
// settings.json and, implicitly, by leaving raw (which still has it)
// untouched for the backup.
const utf8BOM = "\xEF\xBB\xBF"

// PatchResult reports what Patch did.
type PatchResult struct {
	// Changed is true when settings.json's content was modified. It is
	// false when the fallback was already present everywhere it belongs
	// (Patch is idempotent) and the file was left untouched.
	Changed bool
	// BackupPath is the backup Patch created or found already present.
	// Empty when Changed is false, since nothing needed backing up.
	BackupPath string
	Fallback   string
}

// Version detection: plan.md section 5 asks to skip patching Windows
// Terminal installs older than 1.21 (they predate the font.face fallback
// list) "if cheaply possible". There is no cheap way to learn a Terminal
// install's version from settings.json or its path alone: the packaged
// installs' version lives in the AppX manifest (readable only through the
// IAppxManifestReader COM API or `Get-AppxPackage`, both far from cheap),
// and the unpackaged build has no version marker on disk at all. Patch
// therefore does not attempt version detection and always writes the
// fallback list. This is harmless on Terminal < 1.21: unknown JSON keys
// are ignored, so the write is simply inert until the user upgrades.

// Patch finds (or creates) profiles.defaults.font.face in path, appends
// ", "+fallback to it unless already present, and does the same for every
// entry in profiles.list that already has its own font.face override. A
// legacy top-level "fontFace" key (pre-1.19 schema) is migrated into
// font.face first, at both the defaults and per-profile level.
//
// path is backed up to path+".devpit-bak" before the first write (never
// clobbering an existing backup) and the new content is written
// atomically (temp file + rename).
//
// If path cannot be parsed as HuJSON, or its top-level value is not a
// JSON object, Patch returns *UnparsableError and does not touch the
// file. Use ManualSnippet to show the user what to paste in by hand.
func Patch(path string, fallback string) (PatchResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return PatchResult{}, fmt.Errorf("wt: read %s: %w", path, err)
	}

	hasBOM := bytes.HasPrefix(raw, []byte(utf8BOM))
	content := raw
	if hasBOM {
		content = raw[len(utf8BOM):]
	}

	// hujson.Parse's tree holds Extra (comment/whitespace) slices that
	// alias the input's backing array, and Value.Format below mutates
	// those slices in place. Parsing a copy keeps raw pristine for the
	// no-op comparison and the backup below.
	parseInput := make([]byte, len(content))
	copy(parseInput, content)

	root, err := hujson.Parse(parseInput)
	if err != nil {
		return PatchResult{}, &UnparsableError{Path: path, Err: err}
	}
	rootObj, ok := root.Value.(*hujson.Object)
	if !ok {
		return PatchResult{}, &UnparsableError{Path: path, Err: errors.New("top-level value is not a JSON object")}
	}

	// changed tracks whether any mutation below actually altered the
	// value, as opposed to a re-formatting difference: Format (below)
	// normalizes whitespace unconditionally (e.g. spaces to tabs), so
	// comparing Pack() output against raw would report a change on every
	// call to an already-patched file that simply used a different
	// indentation style. Tracking semantic changes directly keeps Patch
	// idempotent and keeps a no-op call from touching the file at all.
	var changed bool

	c, err := migrateLegacyFontFace(rootObj)
	if err != nil {
		return PatchResult{}, &UnparsableError{Path: path, Err: err}
	}
	changed = changed || c

	profiles, err := ensureObject(rootObj, "profiles")
	if err != nil {
		return PatchResult{}, &UnparsableError{Path: path, Err: err}
	}
	defaults, err := ensureObject(profiles, "defaults")
	if err != nil {
		return PatchResult{}, &UnparsableError{Path: path, Err: err}
	}
	c, err = migrateLegacyFontFace(defaults)
	if err != nil {
		return PatchResult{}, &UnparsableError{Path: path, Err: err}
	}
	changed = changed || c

	defaultsFont, err := ensureObject(defaults, "font")
	if err != nil {
		return PatchResult{}, &UnparsableError{Path: path, Err: err}
	}
	c, err = setDefaultsFace(defaultsFont, fallback, "Cascadia Mono")
	if err != nil {
		return PatchResult{}, &UnparsableError{Path: path, Err: err}
	}
	changed = changed || c

	c, err = patchProfileList(profiles, fallback)
	if err != nil {
		return PatchResult{}, &UnparsableError{Path: path, Err: err}
	}
	changed = changed || c

	if !changed {
		return PatchResult{Changed: false, Fallback: fallback}, nil
	}

	root.Format()
	newContent := root.Pack()
	if hasBOM {
		newContent = append([]byte(utf8BOM), newContent...)
	}

	backupPath := path + backupSuffix
	if _, statErr := os.Stat(backupPath); errors.Is(statErr, os.ErrNotExist) {
		if err := writeFileAtomic(backupPath, raw, 0o644); err != nil {
			return PatchResult{}, fmt.Errorf("wt: write backup %s: %w", backupPath, err)
		}
	} else if statErr != nil {
		return PatchResult{}, fmt.Errorf("wt: stat backup %s: %w", backupPath, statErr)
	}

	if err := writeFileAtomic(path, newContent, 0o644); err != nil {
		return PatchResult{}, fmt.Errorf("wt: write %s: %w", path, err)
	}

	return PatchResult{Changed: true, BackupPath: backupPath, Fallback: fallback}, nil
}

// Restore overwrites path with its path+".devpit-bak" backup.
func Restore(path string) error {
	backupPath := path + backupSuffix
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("wt: read backup %s: %w", backupPath, err)
	}
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		return fmt.Errorf("wt: restore %s: %w", path, err)
	}
	return nil
}

// ManualSnippet returns the JSON the user can paste into
// profiles.defaults.font in settings.json by hand, for use when Patch
// returns *UnparsableError.
func ManualSnippet(fallback string) string {
	return fmt.Sprintf(`{
    "profiles": {
        "defaults": {
            "font": {
                "face": "Cascadia Mono, %s"
            }
        }
    }
}`, fallback)
}

// patchProfileList appends fallback to font.face for every entry in
// profiles.list that already overrides it. It never creates a font.face
// override for a profile that did not already have one: an untouched
// profile keeps inheriting profiles.defaults.font.face, which Patch has
// already updated. It reports whether it changed anything.
func patchProfileList(profiles *hujson.Object, fallback string) (bool, error) {
	m, ok := findMember(profiles, "list")
	if !ok {
		return false, nil
	}
	arr, ok := m.Value.Value.(*hujson.Array)
	if !ok {
		return false, fmt.Errorf(`wt: "profiles.list" is not an array`)
	}
	var changed bool
	for i := range arr.Elements {
		profObj, ok := arr.Elements[i].Value.(*hujson.Object)
		if !ok {
			continue
		}
		c, err := migrateLegacyFontFace(profObj)
		if err != nil {
			return false, err
		}
		changed = changed || c

		fm, ok := findMember(profObj, "font")
		if !ok {
			continue
		}
		fontObj, ok := fm.Value.Value.(*hujson.Object)
		if !ok {
			continue
		}
		c, err = appendExistingFace(fontObj, fallback)
		if err != nil {
			return false, err
		}
		changed = changed || c
	}
	return changed, nil
}

// migrateLegacyFontFace moves obj's legacy top-level "fontFace" string
// (the pre-1.19 Windows Terminal schema) into obj.font.face. If font.face
// already exists, the legacy value is simply dropped in its favor. It
// reports whether it changed anything (i.e. whether "fontFace" was
// present at all).
func migrateLegacyFontFace(obj *hujson.Object) (bool, error) {
	m, ok := findMember(obj, "fontFace")
	if !ok {
		return false, nil
	}
	lit, ok := m.Value.Value.(hujson.Literal)
	if !ok || lit.Kind() != '"' {
		return false, errors.New(`wt: "fontFace" is not a string`)
	}
	legacy := lit.String()
	removeMember(obj, "fontFace")

	fontObj, err := ensureObject(obj, "font")
	if err != nil {
		return false, err
	}
	if _, ok := findMember(fontObj, "face"); !ok {
		fontObj.Members = append(fontObj.Members, hujson.ObjectMember{
			Name:  hujson.Value{Value: hujson.String("face")},
			Value: hujson.Value{Value: hujson.String(legacy)},
		})
	}
	return true, nil
}

// setDefaultsFace ensures fontObj has a "face" member, seeding it with
// base when absent, then appends fallback to whichever value it now holds
// (unless fallback is already present). It reports whether it changed
// anything.
func setDefaultsFace(fontObj *hujson.Object, fallback, base string) (bool, error) {
	m, ok := findMember(fontObj, "face")
	if !ok {
		fontObj.Members = append(fontObj.Members, hujson.ObjectMember{
			Name:  hujson.Value{Value: hujson.String("face")},
			Value: hujson.Value{Value: hujson.String(appendFallback(base, fallback))},
		})
		return true, nil
	}
	return appendFallbackToFace(m, fallback)
}

// appendExistingFace appends fallback to fontObj's "face" value if it has
// one; it is a no-op when fontObj has no "face" member.
func appendExistingFace(fontObj *hujson.Object, fallback string) (bool, error) {
	m, ok := findMember(fontObj, "face")
	if !ok {
		return false, nil
	}
	return appendFallbackToFace(m, fallback)
}

// appendFallbackToFace appends fallback to m's string value in place
// (preserving its surrounding comments) and reports whether that value
// actually changed.
func appendFallbackToFace(m *hujson.ObjectMember, fallback string) (bool, error) {
	lit, ok := m.Value.Value.(hujson.Literal)
	if !ok || lit.Kind() != '"' {
		return false, fmt.Errorf(`wt: "face" is not a string`)
	}
	current := lit.String()
	updated := appendFallback(current, fallback)
	if updated == current {
		return false, nil
	}
	m.Value.Value = hujson.String(updated)
	return true, nil
}

// appendFallback appends ", "+fallback to the comma-separated font list
// current, unless fallback is already one of its entries.
func appendFallback(current, fallback string) string {
	for _, part := range strings.Split(current, ",") {
		if strings.TrimSpace(part) == fallback {
			return current
		}
	}
	if current == "" {
		return fallback
	}
	return current + ", " + fallback
}

// findMember returns a pointer to obj's member named name, if any. The
// pointer aliases obj.Members so mutating *ObjectMember.Value in place
// (rather than replacing the ObjectMember) preserves that value's
// surrounding comments.
func findMember(obj *hujson.Object, name string) (*hujson.ObjectMember, bool) {
	for i := range obj.Members {
		if memberName(&obj.Members[i]) == name {
			return &obj.Members[i], true
		}
	}
	return nil, false
}

func memberName(m *hujson.ObjectMember) string {
	lit, ok := m.Name.Value.(hujson.Literal)
	if !ok {
		return ""
	}
	return lit.String()
}

// removeMember deletes obj's member named name, if any.
func removeMember(obj *hujson.Object, name string) {
	for i := range obj.Members {
		if memberName(&obj.Members[i]) == name {
			obj.Members = append(obj.Members[:i], obj.Members[i+1:]...)
			return
		}
	}
}

// ensureObject returns the JSON object member named name under obj,
// creating an empty one if absent. It errors if a member with that name
// already exists but is not an object.
func ensureObject(obj *hujson.Object, name string) (*hujson.Object, error) {
	if m, ok := findMember(obj, name); ok {
		sub, ok := m.Value.Value.(*hujson.Object)
		if !ok {
			return nil, fmt.Errorf("wt: %q is not a JSON object", name)
		}
		return sub, nil
	}
	sub := &hujson.Object{}
	obj.Members = append(obj.Members, hujson.ObjectMember{
		Name:  hujson.Value{Value: hujson.String(name)},
		Value: hujson.Value{Value: sub},
	})
	return sub, nil
}
