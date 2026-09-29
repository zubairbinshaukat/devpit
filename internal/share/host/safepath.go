package host

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrUnsafeFolder means the folder must not be shared. The error's text
// says why, in words a person can act on.
var ErrUnsafeFolder = errors.New("that folder cannot be shared")

// localFolder is a folder on one of this PC's drives: a letter, a colon and a
// backslash.
var localFolder = regexp.MustCompile(`^[A-Za-z]:\\`)

// winPath normalises a Windows path for comparison: forward slashes become
// backslashes, case is dropped and trailing backslashes go. It is written out
// rather than using path/filepath so it means the same whatever system runs
// the tests.
func winPath(p string) string {
	return strings.TrimRight(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), "/", `\`)), `\`)
}

// inside reports whether p is dir or a folder under it. Both are normalised.
func inside(p, dir string) bool { return dir != "" && (p == dir || strings.HasPrefix(p, dir+`\`)) }

// CheckFolder refuses the folders that must never be shared, before anything
// is set up and before the admin prompt:
//
//   - anything that is not a folder on this PC's own drives;
//   - a whole drive. Sharing one would also give the temporary login read
//     access to every file on it, and writing that permission walks the
//     entire drive;
//   - Windows, Program Files and ProgramData, and everything in them;
//   - the user's own profile folder and the folders above it, which hold
//     every account's AppData: saved browser sign-ins, tokens and keys;
//   - AppData and .ssh inside the profile.
//
// A folder inside the profile, such as Documents\Games, is fine. getenv reads
// the environment; tests pass their own.
func CheckFolder(p string, getenv func(string) string) error {
	refuse := func(why string) error { return fmt.Errorf("%w: %s", ErrUnsafeFolder, why) }
	if !localFolder.MatchString(strings.TrimSpace(p)) || strings.Contains(p, "..") {
		return refuse("choose a folder on one of this PC's own drives")
	}
	clean := winPath(p)
	if len(clean) <= 2 {
		return refuse("a whole drive cannot be shared. Choose a folder on it")
	}
	for _, env := range []string{"WINDIR", "SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "ProgramData"} {
		if inside(clean, winPath(getenv(env))) {
			return refuse("that is a Windows or program folder. Choose a folder with your own files")
		}
	}
	profile := winPath(getenv("USERPROFILE"))
	if profile == "" {
		return nil
	}
	if clean == profile || strings.HasPrefix(profile, clean+`\`) {
		return refuse("that folder holds your whole user profile, with saved passwords and keys. Choose a folder inside it, such as Documents")
	}
	// Every account's profile sits next to this one (C:\Users\<name>), and
	// the same rules hold for all of them.
	users := profile[:max(strings.LastIndex(profile, `\`), 0)]
	if len(users) > 2 && strings.HasPrefix(clean, users+`\`) {
		parts := strings.Split(clean[len(users)+1:], `\`)
		switch {
		case len(parts) == 1:
			return refuse("that is a whole user profile, with saved passwords and keys. Choose a folder inside it, such as Documents")
		case parts[1] == "appdata" || parts[1] == ".ssh":
			return refuse("that folder holds app data, saved passwords or keys. Choose a folder with your own files")
		}
	}
	return nil
}
