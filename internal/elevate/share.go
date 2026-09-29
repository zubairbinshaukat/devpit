package elevate

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
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
	// pathPattern is a local absolute path with none of the characters a
	// quote, a wildcard or a stream could smuggle in.
	pathPattern = regexp.MustCompile(`^[A-Za-z]:\\[^<>:"|?*'\x00-\x1f]*$`)
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
	return strings.TrimRight(strings.ToLower(strings.ReplaceAll(p, "/", `\`)), `\`)
}

// validateSharePath refuses folders that must never be shared: the Windows
// directory, Program Files, the shared program data and the root of the
// system drive.
func validateSharePath(p string) error {
	clean := winPath(p)
	for _, env := range []string{"WINDIR", "ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
		root := winPath(os.Getenv(env))
		if root != "" && (clean == root || strings.HasPrefix(clean, root+`\`)) {
			return fmt.Errorf("refused: %q is a system folder", p)
		}
	}
	if sys := winPath(os.Getenv("SystemDrive")); sys != "" && clean == sys {
		return errors.New("refused: the whole system drive cannot be shared")
	}
	return nil
}

// UserManager is what the worker needs to make and remove the temporary
// account. The default executor implements it with the Windows account
// functions; tests use a fake. It is kept apart from [Executor] so that
// interface stays as it is.
type UserManager interface {
	// CreateUser adds a local account with the given password, expiring at
	// expires, and puts it in the Users group.
	CreateUser(name, password string, expires time.Time) error
	// DeleteUser removes a local account.
	DeleteUser(name string) error
	// UserSID returns the account's SID as text.
	UserSID(name string) (string, error)
}

// shareState is what one worker connection has set up so far, and so what
// stop has to undo. It lives only in the worker's memory; the TUI keeps its
// own copy, without the password, in a manifest for the day both die.
type shareState struct {
	profileIndex    uint32
	profileOriginal string
	rules           []string
	user            string
	sid             string
	grantPath       string
	shareName       string
}

// empty reports whether there is nothing to undo.
func (s *shareState) empty() bool {
	return s.profileIndex == 0 && len(s.rules) == 0 && s.user == "" &&
		s.grantPath == "" && s.shareName == ""
}

// shareSession runs the share operations of one worker connection.
type shareSession struct {
	exec  Executor
	users UserManager
	state shareState
	now   func() time.Time
	// stat is os.Stat, replaced in tests so no real folder is needed.
	stat func(string) (os.FileInfo, error)
}

// newShareSession returns a session over exec. The account operations need
// exec to be a [UserManager]; without it they are refused.
func newShareSession(exec Executor) *shareSession {
	um, _ := exec.(UserManager)
	return &shareSession{exec: exec, users: um, now: time.Now, stat: os.Stat}
}

// psExe and icaclsExe are the full paths of the two programs the worker may
// start, built from %WINDIR% so a program of the same name earlier in PATH
// can never be picked up by an elevated process.
func psExe() string {
	return filepath.Join(os.Getenv("WINDIR"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

// icaclsExe is the full path of icacls.exe.
func icaclsExe() string { return filepath.Join(os.Getenv("WINDIR"), "System32", "icacls.exe") }

// psArgv wraps script as an -EncodedCommand command line. The script is
// passed as base64 of UTF-16, which leaves nothing for a command line parser
// to misread, whatever the checked values inside it contain.
func psArgv(script string) []string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = binary.LittleEndian.AppendUint16(b, c)
	}
	return []string{
		psExe(), "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand",
		base64.StdEncoding.EncodeToString(b),
	}
}

// scriptProfile is the profile switch. %d and %s are a checked index and a
// category from a two-value list.
const scriptProfile = `$ErrorActionPreference='Stop'
Set-NetConnectionProfile -InterfaceIndex %d -NetworkCategory %s`

// scriptFirewallOn turns on the SMB inbound rule of the File and Printer
// Sharing group. The group is named by its resource string
// @FirewallAPI.dll,-28502, which is the language-independent form of the
// group's Group property; DisplayGroup would be "File and Printer Sharing"
// only on an English Windows. Only the rule named FPS-SMB-In-TCP is switched
// on when it exists, because the whole group also opens the print spooler and
// name lookup; the group as a whole is the fallback when Windows names it
// otherwise. Each rule that was off and is now on is printed as FW=<name>, so
// stop turns off exactly those and leaves rules the user had on alone.
const scriptFirewallOn = `$ErrorActionPreference='Stop'
$all=@(Get-NetFirewallRule -Group '@FirewallAPI.dll,-28502')
$pick=@($all | Where-Object { $_.Name -like 'FPS-SMB-In-TCP*' })
if($pick.Count -eq 0){ $pick=$all }
foreach($r in $pick){
  if("$($r.Enabled)" -ne 'True'){
    Set-NetFirewallRule -Name $r.Name -Enabled True
    Write-Output ('FW=' + $r.Name)
  }
}`

// scriptFirewallOff is the reverse, for the rules named FW= on the way in.
const scriptFirewallOff = `$ErrorActionPreference='Stop'
Set-NetFirewallRule -Name %s -Enabled False`

// scriptShare creates the read-only share and prints who can reach it, one
// ACE= line each, so the worker can check that nobody but the temporary
// account has access. New-SmbShare with only -ReadAccess grants nothing to
// anyone else.
const scriptShare = `$ErrorActionPreference='Stop'
$u=$env:COMPUTERNAME + '\' + '%s'
New-SmbShare -Name '%s' -Path '%s' -ReadAccess $u -Description '%s' | Out-Null
Get-SmbShareAccess -Name '%s' | ForEach-Object { Write-Output ('ACE=' + $_.AccountName + '|' + $_.AccessRight + '|' + $_.AccessControlType) }`

// scriptUnshare removes a share, only when it carries Devpit's description.
const scriptUnshare = `$ErrorActionPreference='Stop'
$s=Get-SmbShare -Name '%s' -ErrorAction SilentlyContinue
if($s -and $s.Description -eq '%s'){ Remove-SmbShare -Name '%s' -Force }`

// onLine is the callback that carries a line of worker output to the TUI.
type onLine func(stream, text string)

// run runs one operation, sending progress lines through emit. It returns a
// sentence for the TUI when the operation failed.
func (s *shareSession) run(ctx context.Context, req ShareRequest, emit onLine) error {
	if err := req.Validate(); err != nil {
		return err
	}
	switch req.Op {
	case ShareOpProfile:
		return s.opProfile(ctx, req, emit)
	case ShareOpFirewall:
		return s.opFirewall(ctx, emit)
	case ShareOpAccount:
		return s.opAccount(req, emit)
	case ShareOpGrant:
		return s.opGrant(ctx, req, emit)
	case ShareOpShare:
		return s.opShare(ctx, req, emit)
	case ShareOpStop:
		return s.teardown(ctx, emit)
	case ShareOpCleanup:
		s.seed(req)
		return s.teardown(ctx, emit)
	default:
		return fmt.Errorf("refused: unknown share operation %q", req.Op)
	}
}

// ps runs a PowerShell script and returns its stdout lines. A non-zero exit
// is an error carrying the last error line.
func (s *shareSession) ps(ctx context.Context, script string) ([]string, error) {
	var out, errLines []string
	code, err := s.exec.Exec(ctx, psArgv(script), func(stream, text string) {
		if stream == "stderr" {
			errLines = append(errLines, text)
			return
		}
		out = append(out, text)
	})
	if err != nil {
		return out, err
	}
	if code != 0 {
		return out, fmt.Errorf("powershell exited with %d: %s", code, lastNonEmpty(errLines, out))
	}
	return out, nil
}

// lastNonEmpty picks the most useful line to quote in an error.
func lastNonEmpty(lists ...[]string) string {
	for _, l := range lists {
		for i := len(l) - 1; i >= 0; i-- {
			if t := strings.TrimSpace(l[i]); t != "" {
				return t
			}
		}
	}
	return ""
}

// opProfile switches the network category and remembers how to switch back.
func (s *shareSession) opProfile(ctx context.Context, req ShareRequest, emit onLine) error {
	emit("info", "Switching the network to "+req.Category)
	if _, err := s.ps(ctx, fmt.Sprintf(scriptProfile, req.Index, req.Category)); err != nil {
		return err
	}
	s.state.profileIndex, s.state.profileOriginal = req.Index, req.Original
	return nil
}

// opFirewall turns on the SMB rule and records which rules it changed.
func (s *shareSession) opFirewall(ctx context.Context, emit onLine) error {
	emit("info", "Allowing file sharing through the firewall")
	out, err := s.ps(ctx, scriptFirewallOn)
	if err != nil {
		return err
	}
	for _, l := range out {
		if name, ok := strings.CutPrefix(strings.TrimSpace(l), "FW="); ok && rulePattern.MatchString(name) {
			s.state.rules = append(s.state.rules, name)
			emit("info", "FW="+name)
		}
	}
	return nil
}

// opAccount creates the temporary account natively, so the password is never
// on a command line, and reports its SID.
func (s *shareSession) opAccount(req ShareRequest, emit onLine) error {
	if s.users == nil {
		return errors.New("this worker cannot create accounts")
	}
	emit("info", "Creating a temporary login")
	if err := s.users.CreateUser(req.User, req.Password, s.now().Add(AccountLifetime)); err != nil {
		return fmt.Errorf("creating the account: %w", err)
	}
	s.state.user = req.User
	if sid, err := s.users.UserSID(req.User); err == nil && sidPattern.MatchString(sid) {
		s.state.sid = sid
		emit("info", "SID="+sid)
	}
	return nil
}

// opGrant gives the account read access to the folder, inherited by
// everything in it. Share permissions and file permissions both have to
// allow a read, and a folder under a user profile grants nobody else any.
func (s *shareSession) opGrant(ctx context.Context, req ShareRequest, emit onLine) error {
	if st, err := s.stat(req.Path); err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a folder that exists", req.Path)
	}
	emit("info", "Letting the temporary login read the folder")
	argv := []string{icaclsExe(), req.Path, "/grant", req.User + ":(OI)(CI)RX"}
	code, err := s.exec.Exec(ctx, argv, nil)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("icacls exited with %d", code)
	}
	s.state.grantPath = req.Path
	return nil
}

// opShare creates the share and checks that only the account can reach it.
func (s *shareSession) opShare(ctx context.Context, req ShareRequest, emit onLine) error {
	emit("info", "Sharing the folder read-only")
	script := fmt.Sprintf(scriptShare, req.User, req.Name, req.Path, shareDescription, req.Name)
	out, err := s.ps(ctx, script)
	if err != nil {
		return err
	}
	s.state.shareName = req.Name
	if err := checkACL(out, req.User); err != nil {
		return err
	}
	return nil
}

// checkACL verifies the share's access list: exactly the account, allowed,
// with Read and nothing more. Anything else means Windows did not do what
// was asked, and the share must not stay open.
func checkACL(lines []string, user string) error {
	n := 0
	for _, l := range lines {
		rest, ok := strings.CutPrefix(strings.TrimSpace(l), "ACE=")
		if !ok {
			continue
		}
		n++
		f := strings.Split(rest, "|")
		if len(f) != 3 || !strings.HasSuffix(strings.ToLower(f[0]), `\`+user) ||
			f[1] != "Read" || f[2] != "Allow" {
			return fmt.Errorf("the share has an unexpected access entry %q", rest)
		}
	}
	if n == 0 {
		return errors.New("could not confirm who can reach the share")
	}
	return nil
}

// seed loads a manifest's record into the state, so teardown can undo it.
func (s *shareSession) seed(r ShareRequest) {
	s.state = shareState{
		profileIndex: r.Index, profileOriginal: r.Original,
		rules: append([]string(nil), r.Rules...),
		user:  r.User, sid: r.SID, grantPath: r.Path, shareName: r.Name,
	}
}

// teardown undoes everything in state, newest first: the share, the folder
// permission, the account, the firewall rules, the network category. It goes
// on past a failure so one stuck step does not leave the others behind, and
// keeps a step in state until it has really succeeded, so a second stop
// finishes the job. The errors are joined.
func (s *shareSession) teardown(ctx context.Context, emit onLine) error {
	if s.state.empty() {
		return nil
	}
	emit("info", "Cleaning up")
	var errs []error
	st := &s.state

	if st.shareName != "" {
		script := fmt.Sprintf(scriptUnshare, st.shareName, shareDescription, st.shareName)
		if _, err := s.ps(ctx, script); err != nil {
			errs = append(errs, fmt.Errorf("removing the share: %w", err))
		} else {
			st.shareName = ""
		}
	}
	if st.grantPath != "" {
		who := st.user
		if st.sid != "" {
			who = "*" + st.sid
		}
		code, err := s.exec.Exec(ctx, []string{icaclsExe(), st.grantPath, "/remove:g", who}, nil)
		if err != nil || code != 0 {
			errs = append(errs, fmt.Errorf("removing the folder permission: %w", firstErr(err, code)))
		} else {
			st.grantPath = ""
		}
	}
	if st.user != "" && s.users != nil {
		if err := s.users.DeleteUser(st.user); err != nil && !isMissingUser(err) {
			errs = append(errs, fmt.Errorf("removing the account: %w", err))
		} else {
			st.user, st.sid = "", ""
		}
	}
	if len(st.rules) > 0 {
		quoted := make([]string, len(st.rules))
		for i, r := range st.rules {
			quoted[i] = "'" + r + "'"
		}
		if _, err := s.ps(ctx, fmt.Sprintf(scriptFirewallOff, strings.Join(quoted, ","))); err != nil {
			errs = append(errs, fmt.Errorf("restoring the firewall: %w", err))
		} else {
			st.rules = nil
		}
	}
	if st.profileIndex != 0 {
		script := fmt.Sprintf(scriptProfile, st.profileIndex, st.profileOriginal)
		if _, err := s.ps(ctx, script); err != nil {
			errs = append(errs, fmt.Errorf("restoring the network category: %w", err))
		} else {
			st.profileIndex, st.profileOriginal = 0, ""
		}
	}
	return errors.Join(errs...)
}

// firstErr picks the error to quote for a command that may have failed by
// error or by exit code.
func firstErr(err error, code int) error {
	if err != nil {
		return err
	}
	return errors.New("exit code " + strconv.Itoa(code))
}

// teardownTimeout bounds the automatic cleanup when the connection breaks.
const teardownTimeout = 2 * time.Minute

// autoTeardown is the worker's own cleanup when its connection to the TUI
// ends for any reason: a stop already ran (nothing left to do), the TUI
// quit, or the TUI was killed. It is what makes "close Devpit" and "Devpit
// crashed" leave no share, account or firewall change behind.
func (s *shareSession) autoTeardown() {
	if s.state.empty() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()
	_ = s.teardown(ctx, func(string, string) {})
}
