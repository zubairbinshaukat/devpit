package elevate

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

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

// sidResolver says whose a SID is. A cleanup request carries a SID read
// from a manifest on disk, and the worker only removes a folder permission
// for it once it knows the SID is the temporary account's or nobody's; see
// [shareSession.grantee].
type sidResolver interface {
	// SIDAccount returns the account name of sid, or mapped=false when no
	// account on this PC has it.
	SIDAccount(sid string) (name string, mapped bool, err error)
}

// shareState is what one worker connection has set up so far, and so what
// stop has to undo. It lives only in the worker's memory; the TUI keeps its
// own copy, without the password, in a manifest for the day both die.
//
// A step is recorded before its command runs, not after, wherever undoing a
// step that never happened is harmless: a command that is cancelled or times
// out may already have done its work, and a step that is not recorded is
// never undone.
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

// shareSession runs the share operations of one worker connection, one at a
// time: each holds the session's lane while it runs, and it is the lane, not
// the worker's read loop, that serialises them, so a stuck operation never
// stops the worker reading a cancel or a shutdown.
type shareSession struct {
	exec  Executor
	users UserManager
	sids  sidResolver
	state shareState
	now   func() time.Time
	// stat is os.Stat, replaced in tests so no real folder is needed.
	stat func(string) (os.FileInfo, error)
	// resolve is [finalPath], replaced in tests.
	resolve func(string) (string, error)
	// lane is held by the operation that is running.
	lane chan struct{}
}

// newShareSession returns a session over exec. The account operations need
// exec to be a [UserManager]; without it they are refused.
func newShareSession(exec Executor) *shareSession {
	um, _ := exec.(UserManager)
	sr, _ := exec.(sidResolver)
	return &shareSession{
		exec: exec, users: um, sids: sr, now: time.Now, stat: os.Stat,
		resolve: finalPath, lane: make(chan struct{}, 1),
	}
}

// acquire takes the lane, or gives up when ctx ends first.
func (s *shareSession) acquire(ctx context.Context) error {
	select {
	case s.lane <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		s.release()
		return err
	}
	return nil
}

// release gives the lane back.
func (s *shareSession) release() { <-s.lane }

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

// psString is a PowerShell expression whose value is s, written so that
// nothing in s is ever parsed as script: s travels as base64 and is decoded
// by PowerShell itself. Quoting s instead is not safe, because PowerShell
// also ends a single-quoted string at the typographic quotes U+2018, U+2019,
// U+201A and U+201B, which a folder name ("Bob’s Photos") can hold.
func psString(s string) string {
	return "([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" +
		base64.StdEncoding.EncodeToString([]byte(s)) + "')))"
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
// name lookup. The name is matched exactly: a wildcard would also catch
// FPS-SMB-In-TCP-NoScope, the Domain rule that takes SMB from any address.
// The group minus its NoScope rules is the fallback when Windows names the
// rule otherwise. Each rule that was off and is now on is printed as
// FW=<name>, so stop turns off exactly those and leaves rules the user had on
// alone.
const scriptFirewallOn = firewallPick + `
foreach($r in $pick){
  if("$($r.Enabled)" -ne 'True'){
    Set-NetFirewallRule -Name $r.Name -Enabled True
    Write-Output ('FW=' + $r.Name)
  }
}`

// firewallPick is the read-only half of [scriptFirewallOn]: it leaves the
// rules to turn on in $pick. A test runs it alone against the real firewall.
const firewallPick = `$ErrorActionPreference='Stop'
$all=@(Get-NetFirewallRule -Group '@FirewallAPI.dll,-28502')
$pick=@($all | Where-Object { $_.Name -eq 'FPS-SMB-In-TCP' })
if($pick.Count -eq 0){ $pick=@($all | Where-Object { $_.Name -notlike '*NoScope*' }) }`

// scriptFirewallOff is the reverse, for the rules named FW= on the way in.
// The names match rulePattern, which has no quote of any kind.
const scriptFirewallOff = `$ErrorActionPreference='Stop'
Set-NetFirewallRule -Name %s -Enabled False`

// scriptShare creates the read-only share and prints who can reach it, one
// ACE= line each, so the worker can check that nobody but the temporary
// account has access. New-SmbShare with only -ReadAccess grants nothing to
// anyone else. Every %s is a [psString].
//
// Windows 11 24H2 has a second SMB rule, FPS-SMB-In-TCP-V2 in the group
// @FirewallAPI.dll,-28672, for every profile and off by default, which
// creating a share can turn on by itself. The rules of that group that are
// off beforehand and on afterwards are printed as FW=<name> too, so stop
// turns them off again and SMB never stays open on a Public network. The
// group does not exist on older Windows, which is fine.
const scriptShare = `$ErrorActionPreference='Stop'
$u=$env:COMPUTERNAME + '\' + %s
$n=%s
$p=%s
$d=%s
$g='@FirewallAPI.dll,-28672'
$off=@(Get-NetFirewallRule -Group $g -ErrorAction SilentlyContinue | Where-Object { "$($_.Enabled)" -ne 'True' } | ForEach-Object { $_.Name })
try {
  New-SmbShare -Name $n -Path $p -ReadAccess $u -Description $d | Out-Null
} finally {
  foreach($r in @(Get-NetFirewallRule -Group $g -ErrorAction SilentlyContinue)){
    if(($off -contains $r.Name) -and "$($r.Enabled)" -eq 'True'){ Write-Output ('FW=' + $r.Name) }
  }
}
Get-SmbShareAccess -Name $n | ForEach-Object { Write-Output ('ACE=' + $_.AccountName + '|' + $_.AccessRight + '|' + $_.AccessControlType) }`

// scriptUnshare removes a share, only when it carries Devpit's description.
// A share that is not there is not an error. Every %s is a [psString].
const scriptUnshare = `$ErrorActionPreference='Stop'
$n=%s
$d=%s
$s=Get-SmbShare -Name $n -ErrorAction SilentlyContinue
if($s -and $s.Description -eq $d){ Remove-SmbShare -Name $n -Force }`

// scriptOtherShares prints how many Devpit shares other than %s (a
// [psString], empty when this session has none) are up, for instance from a
// second Devpit window. Both %s are [psString]s.
const scriptOtherShares = `$ErrorActionPreference='Stop'
$n=%s
$d=%s
Write-Output ('OTHERS=' + @(Get-SmbShare | Where-Object { $_.Description -eq $d -and $_.Name -ne $n }).Count)`

// errnoNoneMapped is ERROR_NONE_MAPPED, which icacls exits with when the
// account a permission names no longer exists.
const errnoNoneMapped = 1332

// onLine is the callback that carries a line of worker output to the TUI.
type onLine func(stream, text string)

// run runs one operation, sending progress lines through emit. It returns a
// sentence for the TUI when the operation failed. It waits for the lane, so
// operations never overlap, and gives up waiting when ctx ends.
func (s *shareSession) run(ctx context.Context, req ShareRequest, emit onLine) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
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
		if !s.state.empty() {
			return errors.New("refused: a share is still up; stop it before cleaning up an old one")
		}
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
// Only one interface is ever switched per session, and the category to go
// back to is the one recorded first.
func (s *shareSession) opProfile(ctx context.Context, req ShareRequest, emit onLine) error {
	st := &s.state
	if st.profileIndex != 0 && st.profileIndex != req.Index {
		return errors.New("refused: another network's category is already changed; stop sharing first")
	}
	recorded := st.profileIndex == 0
	if recorded {
		st.profileIndex, st.profileOriginal = req.Index, req.Original
	}
	emit("info", "Switching the network to "+req.Category)
	if _, err := s.ps(ctx, fmt.Sprintf(scriptProfile, req.Index, req.Category)); err != nil {
		// A plain failure changed nothing, so there is nothing to put back.
		// A cancel or a time-out may have landed after the switch, so the
		// record stays and stop puts the original category back.
		if recorded && ctx.Err() == nil {
			st.profileIndex, st.profileOriginal = 0, ""
		}
		return err
	}
	return nil
}

// opFirewall turns on the SMB rule and records which rules it changed. The
// rules the script reports are recorded even when it then fails, since
// those were turned on.
func (s *shareSession) opFirewall(ctx context.Context, emit onLine) error {
	emit("info", "Allowing file sharing through the firewall")
	out, err := s.ps(ctx, scriptFirewallOn)
	s.recordRules(out, emit)
	return err
}

// recordRules records every FW=<rule> line of a script's output as a rule
// to turn off again, and passes it on to the TUI for its manifest.
func (s *shareSession) recordRules(out []string, emit onLine) {
	for _, l := range out {
		if name, ok := strings.CutPrefix(strings.TrimSpace(l), "FW="); ok && rulePattern.MatchString(name) {
			if !contains(s.state.rules, name) {
				s.state.rules = append(s.state.rules, name)
			}
			emit("info", "FW="+name)
		}
	}
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, l := range list {
		if l == s {
			return true
		}
	}
	return false
}

// opAccount creates the temporary account natively, so the password is never
// on a command line, and reports its SID. A session makes one account; a
// second would be left behind by stop, which undoes only the one recorded.
func (s *shareSession) opAccount(req ShareRequest, emit onLine) error {
	if s.users == nil {
		return errors.New("this worker cannot create accounts")
	}
	if s.state.user != "" {
		return errors.New("refused: this session already has a temporary login; stop sharing first")
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

// needOwnAccount refuses a grant or a share for any account but the one this
// session created, so the worker never opens a folder to some other devpit-
// account left on the PC.
func (s *shareSession) needOwnAccount(user string) error {
	if s.state.user == "" || !strings.EqualFold(s.state.user, user) {
		return fmt.Errorf("refused: %s is not the temporary login this session created", user)
	}
	return nil
}

// checkFolder refuses a folder that is not there, and one reached through a
// link: a junction, a symbolic link or an 8.3 short name could make a plain
// looking path land in C:\Windows, which [validateSharePath] refuses by
// name. The path must be the folder's own, final name.
func (s *shareSession) checkFolder(p string) error {
	if st, err := s.stat(p); err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a folder that exists", p)
	}
	final, err := s.resolve(p)
	if err != nil {
		return fmt.Errorf("cannot tell where %s leads: %w", p, err)
	}
	if winPath(final) != winPath(p) {
		return fmt.Errorf("refused: %s leads to %s; share that folder by its own name", p, final)
	}
	return validateSharePath(final)
}

// opGrant gives the account read access to the folder, inherited by
// everything in it. Share permissions and file permissions both have to
// allow a read, and a folder under a user profile grants nobody else any.
func (s *shareSession) opGrant(ctx context.Context, req ShareRequest, emit onLine) error {
	if err := s.needOwnAccount(req.User); err != nil {
		return err
	}
	if s.state.grantPath != "" && winPath(s.state.grantPath) != winPath(req.Path) {
		return errors.New("refused: this session already shares another folder; stop sharing first")
	}
	if err := s.checkFolder(req.Path); err != nil {
		return err
	}
	who := req.User
	if s.state.sid != "" {
		who = "*" + s.state.sid
	}
	// Recorded first: removing a grant that was never made is harmless.
	s.state.grantPath = req.Path
	emit("info", "Letting the temporary login read the folder")
	argv := []string{icaclsExe(), req.Path, "/grant", who + ":(OI)(CI)RX"}
	code, err := s.exec.Exec(ctx, argv, nil)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("icacls exited with %d", code)
	}
	return nil
}

// opShare creates the share and checks that only the account can reach it.
func (s *shareSession) opShare(ctx context.Context, req ShareRequest, emit onLine) error {
	if err := s.needOwnAccount(req.User); err != nil {
		return err
	}
	if s.state.shareName != "" {
		return errors.New("refused: this session already has a share; stop sharing first")
	}
	if err := s.checkFolder(req.Path); err != nil {
		return err
	}
	// Recorded first: New-SmbShare may succeed and the access check after it
	// fail or be cut off, and removing a share that is not there, or is not
	// Devpit's, does nothing.
	s.state.shareName = req.Name
	emit("info", "Sharing the folder read-only")
	script := fmt.Sprintf(scriptShare, psString(req.User), psString(req.Name), psString(req.Path), psString(shareDescription))
	out, err := s.ps(ctx, script)
	s.recordRules(out, emit)
	if err != nil {
		return err
	}
	return checkACL(out, req.User)
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

// grantee names whose folder permission teardown removes, in icacls form.
// The SID in the state may have come from a manifest on disk, which anyone
// who can write the user's files can edit, so it is used only when it is
// proven to be the temporary account's: the account's live SID wins; a
// recorded SID is used when it is the account's or belongs to no account at
// all (the account is gone and only its orphaned entry is left). A SID that
// belongs to anyone else is refused, so a cleanup can never strip a real
// user's access to a folder.
func (s *shareSession) grantee() (string, error) {
	st := &s.state
	if st.user != "" && s.users != nil {
		if sid, err := s.users.UserSID(st.user); err == nil && sidPattern.MatchString(sid) {
			return "*" + sid, nil
		}
	}
	if st.sid != "" {
		if s.sids == nil {
			return "", fmt.Errorf("refused: cannot check whose SID %s is", st.sid)
		}
		name, mapped, err := s.sids.SIDAccount(st.sid)
		switch {
		case err != nil:
			return "", fmt.Errorf("checking whose SID %s is: %w", st.sid, err)
		case !mapped:
			return "*" + st.sid, nil
		case st.user != "" && strings.EqualFold(name, st.user):
			return "*" + st.sid, nil
		default:
			return "", fmt.Errorf("refused: SID %s belongs to %s, not to Devpit's temporary login", st.sid, name)
		}
	}
	if st.user != "" {
		return st.user, nil
	}
	return "", errors.New("there is no account to remove the folder permission for")
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
	own := st.shareName

	if st.shareName != "" {
		script := fmt.Sprintf(scriptUnshare, psString(st.shareName), psString(shareDescription))
		if _, err := s.ps(ctx, script); err != nil {
			errs = append(errs, fmt.Errorf("removing the share: %w", err))
		} else {
			st.shareName = ""
		}
	}
	if st.grantPath != "" {
		who, err := s.grantee()
		if err == nil {
			var code int
			code, err = s.exec.Exec(ctx, []string{icaclsExe(), st.grantPath, "/remove:g", who}, nil)
			// 1332: the account is gone, and so is anything to remove.
			if err == nil && code != 0 && code != errnoNoneMapped {
				err = errors.New("exit code " + strconv.Itoa(code))
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("removing the folder permission: %w", err))
		} else {
			st.grantPath = ""
		}
	}
	if st.user != "" && s.users != nil {
		if err := s.users.DeleteUser(st.user); err != nil && !isMissingUser(err) {
			errs = append(errs, fmt.Errorf("removing the account: %w", err))
		} else {
			st.user = ""
		}
	}
	if st.user == "" && st.grantPath == "" {
		// The SID is kept while a folder permission is still to be removed:
		// once the account is gone it is the only name that entry has.
		st.sid = ""
	}
	if len(st.rules) > 0 || st.profileIndex != 0 {
		// The firewall rule and the network category are shared by every
		// Devpit share on this PC. While another one is up (a second Devpit
		// window), putting them back would cut it off, so they are left as
		// they are and no longer this session's to undo.
		others, err := s.otherShares(ctx, own)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("checking for other Devpit shares: %w", err))
			return errors.Join(errs...)
		case others > 0:
			emit("info", "Leaving file sharing allowed in the firewall: another Devpit share is still up")
			st.rules, st.profileIndex, st.profileOriginal = nil, 0, ""
			return errors.Join(errs...)
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

// otherShares counts the Devpit shares that are up besides own.
func (s *shareSession) otherShares(ctx context.Context, own string) (int, error) {
	out, err := s.ps(ctx, fmt.Sprintf(scriptOtherShares, psString(own), psString(shareDescription)))
	if err != nil {
		return 0, err
	}
	for _, l := range out {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "OTHERS="); ok {
			return strconv.Atoi(v)
		}
	}
	return 0, errors.New("the share count was missing from the output")
}

// teardownTimeout bounds the automatic cleanup when the connection breaks.
const teardownTimeout = 2 * time.Minute

// autoTeardown is the worker's own cleanup when its connection to the TUI
// ends for any reason: a stop already ran (nothing left to do), the TUI
// quit, or the TUI was killed. It is what makes "close Devpit" and "Devpit
// crashed" leave no share, account or firewall change behind.
//
// It waits up to wait for the lane, so it never runs beside an operation
// that is still changing the state. An operation that is still holding the
// lane after that ignored its cancel; the cleanup is then left to the
// manifest the TUI keeps, which the next launch offers to finish.
func (s *shareSession) autoTeardown(wait time.Duration) {
	lctx, lcancel := context.WithTimeout(context.Background(), wait)
	defer lcancel()
	if err := s.acquire(lctx); err != nil {
		return
	}
	defer s.release()
	if s.state.empty() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()
	_ = s.teardown(ctx, func(string, string) {})
}
