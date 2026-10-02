package adapters

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// Git reads Devpit's folder rules from files Devpit owns, in
// %USERPROFILE%\.devpit\git:
//
//	rules.gitconfig      [includeIf "gitdir/i:C:/Work/"] blocks, rewritten
//	                     from accounts.toml on every change
//	<identity>.gitconfig [user] name and email (and signing) per identity
//	default.gitconfig    the person's own identity, for a folder whose rule
//	                     says "default" inside a folder ruled otherwise
//	ssh-<id>.gitconfig   core.sshCommand for a folder with its own SSH key
//	devpit.json          what accounts.toml has no field for: signing
//	                     options, per-folder SSH keys and the credential
//	                     helper chain Devpit took over from
//
// and one [include] block at the end of the person's global config. Every
// .gitconfig file Devpit writes starts with gitHeader and ends with a
// checksum line, so a file changed by hand is noticed and never overwritten.

const (
	gitRulesFile   = "rules.gitconfig"
	gitDefaultFile = "default.gitconfig"
	gitExtrasFile  = "devpit.json"
	gitHeader      = "# Written by Devpit."
	gitSumPrefix   = "# devpit:"
	gitIndent      = "    "
	// githubURL is the only credential context Devpit answers for.
	githubURL = "https://github.com"
)

// GitDir is the folder Devpit's Git files live in: next to the accounts
// folder (%USERPROFILE%\.devpit\git), so DEVPIT_ACCOUNTS_DIR moves both.
func GitDir(d Deps) string {
	if d.Paths.AccountsDir != "" {
		return filepath.Join(filepath.Dir(d.Paths.AccountsDir), "git")
	}
	return filepath.Join(d.Home, ".devpit", "git")
}

// gitGlobalPath is the file `git config --global` reads and writes:
// GIT_CONFIG_GLOBAL when set, else ~/.gitconfig (HOME first, as Git for
// Windows does), else the XDG file when only that one exists. A symbolic
// link (a dotfiles manager) is followed so the link itself stays a link.
func gitGlobalPath(d Deps) string {
	if p := d.getenv("GIT_CONFIG_GLOBAL"); p != "" {
		return followLink(p)
	}
	home := d.getenv("HOME")
	if home == "" {
		home = d.getenv("USERPROFILE")
	}
	if home == "" {
		home = d.Home
	}
	main := filepath.Join(home, ".gitconfig")
	if _, err := os.Stat(main); err != nil {
		xdg := d.getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		if alt := filepath.Join(xdg, "git", "config"); fileExists(alt) {
			return followLink(alt)
		}
	}
	return followLink(main)
}

func followLink(p string) string {
	if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if target, err := filepath.EvalSymlinks(p); err == nil {
			return target
		}
	}
	return p
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// slashPath is p with forward slashes, the way Git writes paths.
func slashPath(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// errUnsafeValue is wrapped by the error for a value that cannot be written
// into a Git config file without changing its meaning.
var errUnsafeValue = errors.New("it has a line break or another control character, which would let it add its own lines to Git's config")

// gitQuote returns v as a double-quoted Git config value (or subsection
// name): backslashes and quotes are escaped, so '#', ';', quotes and
// backslashes are plain text. Control characters, a line break above all,
// are refused: there is no way to write one that could not start a new
// line of config.
func gitQuote(v string) (string, error) {
	if !utf8.ValidString(v) {
		return "", fmt.Errorf("%q is not text: %w", v, errUnsafeValue)
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%q: %w", v, errUnsafeValue)
		}
	}
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return `"` + v + `"`, nil
}

// gitdirPattern turns a rule's folder into the pattern after "gitdir/i:":
// forward slashes, the drive letter or //server/share, and a trailing slash
// (Git then adds "**", so the rule covers every repo inside). '[' starts a
// character class in Git's wildmatch, so it is escaped; '*' and '?' cannot
// be in a Windows path.
func gitdirPattern(folder string) (string, error) {
	norm, err := accounts.NormalizeFolder(folder, "")
	if err != nil {
		return "", err
	}
	p := slashPath(norm)
	p = strings.ReplaceAll(p, "[", `\[`)
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p, nil
}

// shellQuote quotes s for the POSIX shell Git runs core.sshCommand through:
// single quotes, with an embedded ' written as '\”.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sshCommand is the core.sshCommand value for one key: the key's path
// with forward slashes, quoted for the shell, and IdentitiesOnly so ssh
// offers only that key.
func sshCommand(keyPath string) string {
	return "ssh -i " + shellQuote(slashPath(keyPath)) + " -o IdentitiesOnly=yes"
}

// gitSum is the checksum line for a Devpit file's body.
func gitSum(body string) string {
	sum := sha256.Sum256([]byte(body))
	return gitSumPrefix + hex.EncodeToString(sum[:8])
}

// sealGitFile ends a Devpit-written file with its checksum line.
func sealGitFile(lines []string) []byte {
	body := strings.Join(lines, "\n") + "\n"
	return []byte(body + gitSum(body) + "\n")
}

// gitFileState says whether a file in Devpit's Git folder is Devpit's and
// unchanged since Devpit wrote it.
type gitFileState int

const (
	gitFileMissing gitFileState = iota
	gitFileOurs                 // Devpit's header and a matching checksum
	gitFileEdited               // Devpit's header, but changed since
	gitFileForeign              // not written by Devpit
)

func classifyGitFile(data []byte) gitFileState {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(s, gitHeader) {
		return gitFileForeign
	}
	t := strings.TrimSuffix(s, "\n")
	i := strings.LastIndexByte(t, '\n')
	if i < 0 {
		return gitFileEdited
	}
	body, last := t[:i+1], t[i+1:]
	if last != gitSum(body) {
		return gitFileEdited
	}
	return gitFileOurs
}

func readGitFile(path string) ([]byte, gitFileState, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- a file in Devpit's own folder
	if errors.Is(err, os.ErrNotExist) {
		return nil, gitFileMissing, nil
	}
	if err != nil {
		return nil, gitFileMissing, err
	}
	return data, classifyGitFile(data), nil
}

// editedError is the error for a Devpit file changed by hand: Devpit stops
// rather than throw the edit away.
func editedError(path string) error {
	return fmt.Errorf("%s was changed by hand since Devpit wrote it, so Devpit stopped and changed nothing. "+
		"Move your own lines to your ~/.gitconfig, then delete %s and try again: %w",
		path, filepath.Base(path), accounts.ErrChangedByHand)
}

// fileLines splits file content into the lines a preview shows.
func fileLines(data []byte) []string {
	s := strings.TrimSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// displayPath shows a path under the home folder as ~/…, with forward
// slashes, the way the plan's previews do.
func displayPath(home, p string) string {
	if home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return "~/" + slashPath(rel)
		}
	}
	return p
}
