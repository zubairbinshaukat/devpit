package elevate

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// KindShare is the job kind of the file sharing operations. Unlike
// [KindExec], it never carries a command: it names one of a short list of
// operations, each with typed arguments that are checked here, in the worker,
// against tight patterns. The command lines the worker runs are built from
// fixed text plus those checked values, so a compromised or buggy TUI cannot
// use the share operations to run anything else as administrator.
const KindShare Kind = "share"

// ShareOp names one file sharing operation.
type ShareOp string

// The operations. Each one is undone by [ShareOpStop], which the worker also
// runs by itself when its connection to the TUI breaks.
const (
	// ShareOpProfile switches a network connection's category (Private or
	// Public) and remembers the original, so stop can put it back.
	ShareOpProfile ShareOp = "profile"
	// ShareOpFirewall turns on the File and Printer Sharing inbound rule
	// for SMB, remembering which rules it changed.
	ShareOpFirewall ShareOp = "firewall"
	// ShareOpAccount creates the temporary local account.
	ShareOpAccount ShareOp = "account"
	// ShareOpGrant gives the temporary account read access to the folder.
	ShareOpGrant ShareOp = "grant"
	// ShareOpShare creates the read-only share.
	ShareOpShare ShareOp = "share"
	// ShareOpStop undoes everything this worker did, in reverse order.
	ShareOpStop ShareOp = "stop"
	// ShareOpCleanup undoes what an earlier run recorded in its manifest,
	// for a run that died without stopping.
	ShareOpCleanup ShareOp = "cleanup"
)

// AccountLifetime is how long the temporary account lives if nothing ever
// removes it. The account expires by itself, so even a power cut in the
// middle of a share leaves nothing usable behind for more than this long.
const AccountLifetime = 48 * time.Hour

// shareDescription marks a share as one Devpit made. The worker only ever
// removes a share that carries it, so a share of the user's own with the
// same name is never touched.
const shareDescription = "devpit-temp-share"

// ShareRequest is the typed argument of a [KindShare] request. Only the
// fields an operation names are read; the rest must be empty.
type ShareRequest struct {
	// Op is the operation.
	Op ShareOp `json:"op"`
	// Index is the network interface index, for profile.
	Index uint32 `json:"index,omitempty"`
	// Category is the category to set, Private or Public, for profile.
	Category string `json:"category,omitempty"`
	// Original is the category to restore on stop, for profile.
	Original string `json:"original,omitempty"`
	// User is the temporary account name, devpit-xxxx, for account, grant,
	// share and cleanup.
	User string `json:"user,omitempty"`
	// Password is the temporary account's password, for account only. It
	// crosses the local pipe once, is handed to Windows in memory, and is
	// never put on a command line or in a log.
	Password string `json:"password,omitempty"`
	// SID is the account's security identifier, for cleanup.
	SID string `json:"sid,omitempty"`
	// Path is the folder to share, for grant, share and cleanup.
	Path string `json:"path,omitempty"`
	// Name is the share name, for share and cleanup.
	Name string `json:"name,omitempty"`
	// Rules are the firewall rules an earlier run turned on, for cleanup.
	Rules []string `json:"rules,omitempty"`
}

// String describes the request without its password, so it is safe in any
// log or error message.
func (r ShareRequest) String() string {
	return fmt.Sprintf("share %s (user %s, path %s, name %s)", r.Op, r.User, r.Path, r.Name)
}

// The patterns every argument must match. They are deliberately narrow: the
// worker creates and deletes only things whose names Devpit generates.
var (
	// userPattern is the only account name the worker will create or delete.
	userPattern = regexp.MustCompile(`^devpit-[a-z0-9]{4,12}$`)
	// namePattern is a share name: plain letters, digits, dash, underscore.
	namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	// sidPattern is a local account SID.
	sidPattern = regexp.MustCompile(`^S-1-5-21(-\d{1,10}){4}$`)
	// rulePattern is a File and Printer Sharing firewall rule name.
	rulePattern = regexp.MustCompile(`^FPS-[A-Za-z0-9_.-]{1,70}$`)
	// pathPattern is a local absolute path with none of the characters
	// Windows forbids in a name, so no wildcard and no stream. An apostrophe
	// is allowed ("Bob's Games"): nothing quotes the path into a script,
	// [psString] carries it.
	pathPattern = regexp.MustCompile(`^[A-Za-z]:\\[^<>:"|?*\x00-\x1f]*$`)
	// passwordPattern is the alphabet [Password] draws from, which is also
	// what the worker accepts: no quote, no space, no shell character.
	passwordPattern = regexp.MustCompile(`^[A-Za-z0-9]{12,64}$`)
)

// categories are the network categories a profile operation may name.
var categories = map[string]bool{"Private": true, "Public": true}

// Validate checks the request against the operation's rules and returns a
// sentence saying what is wrong.
func (r ShareRequest) Validate() error {
	needUser := func() error {
		if !userPattern.MatchString(r.User) {
			return fmt.Errorf("refused: %q is not a Devpit temporary account name", r.User)
		}
		return nil
	}
	needPath := func() error {
		if !pathPattern.MatchString(r.Path) || strings.Contains(r.Path, `..`) {
			return fmt.Errorf("refused: %q is not a plain local folder path", r.Path)
		}
		return validateSharePath(r.Path)
	}
	needName := func() error {
		if !namePattern.MatchString(r.Name) {
			return fmt.Errorf("refused: %q is not a valid share name", r.Name)
		}
		return nil
	}
	switch r.Op {
	case ShareOpProfile:
		if r.Index == 0 || !categories[r.Category] || !categories[r.Original] {
			return errors.New("refused: profile needs an interface index and Private or Public")
		}
	case ShareOpFirewall, ShareOpStop:
	case ShareOpAccount:
		if err := needUser(); err != nil {
			return err
		}
		if !passwordPattern.MatchString(r.Password) {
			return errors.New("refused: the password is not in the allowed form")
		}
	case ShareOpGrant:
		if err := needUser(); err != nil {
			return err
		}
		return needPath()
	case ShareOpShare:
		if err := needUser(); err != nil {
			return err
		}
		if err := needName(); err != nil {
			return err
		}
		return needPath()
	case ShareOpCleanup:
		return r.validateCleanup(needUser, needName, needPath)
	default:
		return fmt.Errorf("refused: unknown share operation %q", r.Op)
	}
	return nil
}

// validateCleanup checks a request that came from a manifest on disk. Every
// field is optional, because a run may have died before creating it, but
// each one present is held to the same pattern as when it was created.
func (r ShareRequest) validateCleanup(needUser, needName, needPath func() error) error {
	if r.User != "" {
		if err := needUser(); err != nil {
			return err
		}
	}
	if r.Name != "" {
		if err := needName(); err != nil {
			return err
		}
	}
	if r.Path != "" {
		if err := needPath(); err != nil {
			return err
		}
	}
	if r.SID != "" && !sidPattern.MatchString(r.SID) {
		return fmt.Errorf("refused: %q is not a local account SID", r.SID)
	}
	for _, rule := range r.Rules {
		if !rulePattern.MatchString(rule) {
			return fmt.Errorf("refused: %q is not a File and Printer Sharing rule", rule)
		}
	}
	if r.Index != 0 || r.Original != "" {
		if r.Index == 0 || !categories[r.Original] {
			return errors.New("refused: the saved network profile is not valid")
		}
	}
	return nil
}

// winPath normalises a Windows path for comparison: forward slashes become
// backslashes, case is dropped and a trailing backslash goes. It is written
// out rather than using path/filepath so the comparison means the same thing
// whatever system runs it, tests included.
func winPath(p string) string {
	return strings.TrimRight(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), "/", `\`)), `\`)
}

// inside reports whether p is dir or a folder under it. Both are normalised.
func inside(p, dir string) bool { return dir != "" && (p == dir || strings.HasPrefix(p, dir+`\`)) }

// validateSharePath refuses the folders that must never be shared. It is the
// worker's own copy of host.CheckFolder (internal/share/host/safepath.go),
// which the TUI runs first; that package imports this one, so the rule is
// duplicated rather than shared, and [TestValidateSharePathMirrorsHostCheckFolder]
// keeps the two saying the same thing. Refused:
//
//   - a whole drive (sharing one grants read on every file, and writing the
//     permission walks the entire drive);
//   - Windows, Program Files and ProgramData, and everything in them;
//   - the user's profile folder and the folders above it, which hold every
//     account's AppData;
//   - any account's whole profile, and AppData and .ssh inside any profile.
//
// pathPattern has already refused UNC paths, relative paths and "..".
func validateSharePath(p string) error {
	clean := winPath(p)
	if len(clean) <= 2 {
		return errors.New("refused: a whole drive cannot be shared")
	}
	for _, env := range []string{"WINDIR", "SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "ProgramData"} {
		if inside(clean, winPath(os.Getenv(env))) {
			return fmt.Errorf("refused: %q is a Windows or program folder", p)
		}
	}
	profile := winPath(os.Getenv("USERPROFILE"))
	if profile == "" {
		return nil
	}
	if clean == profile || strings.HasPrefix(profile, clean+`\`) {
		return fmt.Errorf("refused: %q holds a whole user profile", p)
	}
	users := profile[:max(strings.LastIndex(profile, `\`), 0)]
	if len(users) > 2 && strings.HasPrefix(clean, users+`\`) {
		parts := strings.Split(clean[len(users)+1:], `\`)
		switch {
		case len(parts) == 1:
			return fmt.Errorf("refused: %q is a whole user profile", p)
		case parts[1] == "appdata" || parts[1] == ".ssh":
			return fmt.Errorf("refused: %q holds app data, saved passwords or keys", p)
		}
	}
	return nil
}
