// Package launch starts a developer tool with an account applied to that
// one process. It is shared by devpit-shim (every shimmed call) and by
// "just this once" (devpit <tool> run <name> -- <cmd>).
//
// What it guarantees, each pinned by a test:
//
//   - the real tool is found on PATH with the shim folder skipped, in
//     PATHEXT order with .exe first in each folder;
//   - a shim that would start itself again is caught by a guard variable;
//   - a .cmd or .bat target (npm installs those) is started through cmd.exe
//     with every argument quoted for cmd.exe's own rules, and an argument
//     that cannot be passed safely is refused rather than mangled;
//   - the child shares the console (stdin, stdout, stderr) and receives
//     Ctrl+C and Ctrl+Break itself, while the launcher ignores them;
//   - the child and everything it starts are in a job object that kills
//     them if the launcher is killed, and only then;
//   - the child's exit code is the launcher's exit code.
//
// It imports nothing from Devpit, so the shim stays small.
package launch

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnvGuard is set in the environment of every child a shim starts, as
// "<tool>:<shim pid>". A shim that finds its own tool and its parent's pid
// there was started by a shim for the same tool: it would loop forever, so
// it stops instead.
const EnvGuard = "DEVPIT_SHIM_GUARD"

// EnvOnce is set by `devpit <tool> run <name> -- <command>` for the command
// it starts, as "<tool>:<account name>". A shim for that tool started
// anywhere below it (an npm script that runs claude) uses that account
// instead of the folder's rule, so "just this once" holds for the whole
// command.
const EnvOnce = "DEVPIT_ONCE"

// FindOptions tunes [Find].
type FindOptions struct {
	// Path is the PATH to search; os.Getenv("PATH") when empty.
	Path string
	// PathExt is PATHEXT; os.Getenv("PATHEXT"), or the Windows default,
	// when empty.
	PathExt string
	// Skip lists folders never searched: the shim folder.
	Skip []string
	// Self is the launcher's own executable. A candidate that is the same
	// file is skipped, wherever it is.
	Self string
}

// NotFoundError is returned when name is nowhere on PATH (outside Skip).
type NotFoundError struct{ Name string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s was not found on PATH", e.Name)
}

// Is makes errors.Is(err, exec.ErrNotFound) true.
func (e *NotFoundError) Is(target error) bool { return target == exec.ErrNotFound }

// launchable is the set of extensions Windows can start directly or
// through cmd.exe. A .ps1 or .js on PATHEXT is not started by a shim.
func launchable(ext string) bool {
	switch strings.ToLower(ext) {
	case ".exe", ".com", ".bat", ".cmd":
		return true
	}
	return false
}

// extensions is the order to try in each folder: .exe first, then the rest
// of PATHEXT in its own order, launchable ones only.
func extensions(pathext string) []string {
	if pathext == "" {
		pathext = ".COM;.EXE;.BAT;.CMD"
	}
	out := []string{".exe"}
	for _, e := range strings.Split(pathext, ";") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" || !strings.HasPrefix(e, ".") || !launchable(e) {
			continue
		}
		dup := false
		for _, o := range out {
			if o == e {
				dup = true
			}
		}
		if !dup {
			out = append(out, e)
		}
	}
	return out
}

// dirKey is how two PATH folders are compared: cleaned, unquoted, without a
// trailing separator, ignoring case.
func dirKey(d string) string {
	d = strings.TrimSpace(d)
	d = strings.Trim(d, `"`)
	if d == "" {
		return ""
	}
	d = filepath.Clean(d)
	return strings.ToUpper(strings.TrimRight(d, `\/`))
}

// Find returns the full path of the real program called name, searching
// PATH in order and skipping opts.Skip, opts.Self, relative PATH entries
// (Windows would search those relative to the current folder, which a shim
// must never do), and anything that is not a file. name may carry an
// extension ("claude.exe"); without one, each folder is tried with .exe
// first and then PATHEXT's order.
func Find(name string, opts FindOptions) (string, error) {
	if name == "" || strings.ContainsAny(name, `\/:`) {
		return "", fmt.Errorf("%q is not a program name", name)
	}
	path := opts.Path
	if path == "" {
		path = os.Getenv("PATH")
	}
	pathext := opts.PathExt
	if pathext == "" {
		pathext = os.Getenv("PATHEXT")
	}
	exts := extensions(pathext)
	if ext := filepath.Ext(name); launchable(ext) {
		exts = []string{""}
	}

	skip := map[string]bool{}
	for _, s := range opts.Skip {
		if k := dirKey(s); k != "" {
			skip[k] = true
		}
	}
	var self os.FileInfo
	if opts.Self != "" {
		self, _ = os.Stat(opts.Self)
	}

	for _, dir := range filepath.SplitList(path) {
		dir = strings.Trim(strings.TrimSpace(dir), `"`)
		if dir == "" || !filepath.IsAbs(dir) || skip[dirKey(dir)] {
			continue
		}
		for _, ext := range exts {
			cand := filepath.Join(dir, name+ext)
			fi, err := os.Stat(cand) // #nosec G703 -- a PATH folder plus a program name; nothing is opened
			if err != nil || fi.IsDir() {
				continue
			}
			if self != nil && os.SameFile(fi, self) {
				continue
			}
			return cand, nil
		}
	}
	return "", &NotFoundError{Name: name}
}

// IsBatch reports whether path is a .bat or .cmd script, which Windows runs
// through cmd.exe.
func IsBatch(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".bat", ".cmd":
		return true
	}
	return false
}

// MergeEnv returns base with every key in unset removed and every KEY=VALUE
// in set applied (replacing an existing key). Keys compare ignoring case, as
// Windows does.
func MergeEnv(base, set, unset []string) []string {
	drop := map[string]bool{}
	for _, k := range unset {
		drop[strings.ToUpper(k)] = true
	}
	for _, kv := range set {
		if k, _, ok := strings.Cut(kv, "="); ok {
			drop[strings.ToUpper(k)] = true
		}
	}
	out := make([]string, 0, len(base)+len(set))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		// Windows keeps per-drive current folders as "=C:=C:\x"; keep them.
		if k != "" && drop[strings.ToUpper(k)] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, set...)
}

// ErrUnsafeArgument is wrapped by the error for an argument that cannot be
// passed to a .cmd or .bat script without cmd.exe changing or running it.
var ErrUnsafeArgument = errors.New("this argument cannot be passed safely to a .cmd or .bat program")
