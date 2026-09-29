// Package errmap turns the errors of file sharing into plain words and a next
// step. It is one table: every entry names the Windows error numbers it
// covers, says what went wrong in easy English, and says what to do.
//
// The table is keyed by Win32 error numbers and never by message text.
// Windows prints "System error 53 has occurred" in the language of the PC, so
// a program that looks for the words only works on an English one. The
// numbers are the same everywhere, and [CodeOf] and [CodeFromText] find them
// by structure.
//
// The docs site is generated from [Entries], so the page and the app cannot
// disagree.
package errmap

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// Phase says which step was running when the error came, because a few
// numbers mean different things before and during a copy: error 64 while
// signing in is a wrong address, while copying it is a network that dropped.
type Phase int

// The phases.
const (
	// PhaseConnect is listing shares or signing in.
	PhaseConnect Phase = iota
	// PhaseCopy is the copy itself.
	PhaseCopy
)

// Action is the one button an error may offer next to its text.
type Action string

// The actions a screen knows how to offer.
const (
	// ActionNone offers nothing beyond retry and back.
	ActionNone Action = ""
	// ActionDropConnections offers to close the existing connections to the
	// other PC (`net use \\host /delete`) and try again.
	ActionDropConnections Action = "drop-connections"
	// ActionRetry offers to try again now.
	ActionRetry Action = "retry"
	// ActionChooseFolder offers to pick another destination folder.
	ActionChooseFolder Action = "choose-folder"
)

// Entry is one known problem.
type Entry struct {
	// ID is a stable name, used by tests and by the docs page as an anchor.
	ID string
	// Codes are the Win32 error numbers this entry covers. Several entries
	// may have none: they are reached by a check, not by a number.
	Codes []int
	// Title is the problem in a few words.
	Title string
	// Why says what happened, in short easy sentences.
	Why string
	// Steps is what to do next, one step per item.
	Steps []string
	// Action is the button the screen may offer.
	Action Action
	// Transient marks an error that usually goes away by itself, so a
	// running copy may retry it without asking.
	Transient bool
	// Source names where the number comes from, for the docs page.
	Source string
}

// winerror is the source of every Win32 number below.
const winerror = "Microsoft: System error codes (winerror.h)"

// table is every known problem. Order is the order of the docs page. Entries
// that need a check rather than a number carry no Codes and are found by ID.
var table = []Entry{
	{
		// 3227320323 is 0xC05D0003, STATUS_SMB_GUEST_LOGON_BLOCKED_SIGNING_REQUIRED
		// as `net use` prints it: "System error 3227320323 has occurred."
		ID: "guest-signing", Codes: []int{1272, 3227320323},
		Title: "Windows blocked the sign-in",
		Why: "Windows 11 24H2 does not allow guest sign-in to a shared folder any more. " +
			"It also insists on signed connections.",
		Steps: []string{
			"On the sharing PC, use a Devpit share. It has a real user name and password.",
			"Or type the user name and password of an account on the other PC.",
			"Do not turn off SMB signing to work around this. It protects your files.",
		},
		Source: "Microsoft: SMB signing and guest logons in Windows 11 24H2; reported for `net use` as system error 1272, " +
			"or 3227320323 (0xC05D0003) when signing is required",
	},
	{
		ID: "blank-password", Codes: []int{1327},
		Title: "That account has no password",
		Why:   "Windows does not let an account with a blank password sign in over the network.",
		Steps: []string{
			"On the sharing PC, give that account a password.",
			"Or share with Devpit. It makes a login with a password for you.",
		},
		Source: winerror + ", ERROR_ACCOUNT_RESTRICTION",
	},
	{
		ID: "wrong-password", Codes: []int{86, 1326},
		Title: "The user name or password is wrong",
		Why:   "The other PC did not accept the sign-in.",
		Steps: []string{
			"Check the user name and the password. Letters can be upper or lower case.",
			"Copy them from the Devpit card on the other PC if you can.",
		},
		Source: winerror + ", ERROR_INVALID_PASSWORD and ERROR_LOGON_FAILURE",
	},
	{
		ID: "microsoft-account", Codes: nil,
		Title: "A Microsoft account needs its full name",
		Why:   "This looks like a Microsoft account e-mail. Windows wants it as MicrosoftAccount\\you@example.com.",
		Steps: []string{
			"Type MicrosoftAccount\\ before the e-mail, for example MicrosoftAccount\\you@example.com.",
			"Use the account password. A PIN or fingerprint does not work here.",
		},
		Source: "Microsoft: net use, /user parameter",
	},
	{
		ID: "account-locked", Codes: []int{1909},
		Title:  "That account is locked",
		Why:    "Too many wrong passwords locked the account for a while.",
		Steps:  []string{"Wait a few minutes, or unlock the account on the other PC.", "Then try again."},
		Source: winerror + ", ERROR_ACCOUNT_LOCKED_OUT",
	},
	{
		ID: "no-network-logon", Codes: []int{1385},
		Title:  "That account may not sign in over the network",
		Why:    "The other PC does not give this account the right to sign in from the network.",
		Steps:  []string{"Use another account, or share with Devpit, which sets this up for you."},
		Source: winerror + ", ERROR_LOGON_TYPE_NOT_GRANTED",
	},
	{
		ID: "session-conflict", Codes: []int{1219},
		Title: "Already signed in to that PC as someone else",
		Why: "Windows allows only one sign-in per PC at a time. " +
			"This PC already has a connection to it with other credentials.",
		Steps: []string{
			"Press the button to close the old connections to that PC.",
			"Then sign in again.",
		},
		Action: ActionDropConnections,
		Source: winerror + ", ERROR_SESSION_CREDENTIAL_CONFLICT",
	},
	{
		ID: "not-found", Codes: []int{53, 1203},
		Title: "Cannot find that PC",
		Why:   "Nothing answered at that address.",
		Steps: []string{
			"Check the IP address. Both PCs must be on the same Wi-Fi or network.",
			"On the sharing PC, check that Devpit is still sharing.",
			"A VPN or a guest Wi-Fi can block PCs from seeing each other. Turn it off and try again.",
		},
		Action: ActionRetry,
		Source: winerror + ", ERROR_BAD_NETPATH and ERROR_NO_NET_OR_BAD_PATH",
	},
	{
		ID: "share-not-found", Codes: []int{67},
		Title: "That shared folder is gone",
		Why:   "The other PC answered, but it has no shared folder with that name.",
		Steps: []string{
			"On the sharing PC, check that sharing is still on.",
			"Press r to list the shared folders again.",
		},
		Action: ActionRetry,
		Source: winerror + ", ERROR_BAD_NET_NAME",
	},
	{
		ID: "access-denied", Codes: []int{5},
		Title: "Access denied",
		Why:   "You signed in, but this account may not read that folder.",
		Steps: []string{
			"On the sharing PC, check that the account can read the folder.",
			"A Devpit share does this for you. Start it again there.",
		},
		Source: winerror + ", ERROR_ACCESS_DENIED",
	},
	{
		ID: "network-dropped", Codes: []int{64, 121, 1231},
		Title: "The network dropped",
		Why: "The other PC stopped answering. Wi-Fi may have dropped, " +
			"or the other PC went to sleep or stopped sharing.",
		Steps: []string{
			"Devpit keeps trying and continues when the other PC is back.",
			"Check that the other PC is awake and still sharing.",
		},
		Action:    ActionRetry,
		Transient: true,
		Source:    winerror + ", ERROR_NETNAME_DELETED, ERROR_SEM_TIMEOUT, ERROR_NETWORK_UNREACHABLE",
	},
	{
		ID: "refused", Codes: []int{1225},
		Title: "The other PC refused the connection",
		Why:   "Its firewall or its sharing service said no.",
		Steps: []string{
			"On the sharing PC, check that Devpit is still sharing.",
			"Windows may ask there to allow the connection. Say yes.",
		},
		Action:    ActionRetry,
		Transient: true,
		Source:    winerror + ", ERROR_CONNECTION_REFUSED",
	},
	{
		ID: "share-removed", Codes: nil,
		Title: "The shared folder went away during the copy",
		Why:   "The other PC stopped sharing, or its Devpit was closed.",
		Steps: []string{
			"Start sharing again on the other PC.",
			"Then resume the copy. Finished files are skipped.",
		},
		Action:    ActionRetry,
		Transient: true,
		Source:    "Observed as ERROR_BAD_NET_NAME or ERROR_NETNAME_DELETED while copying",
	},
	{
		ID: "sleeping", Codes: nil,
		Title: "The other PC may be asleep",
		Why:   "It stopped answering. A PC that goes to sleep stops sharing.",
		Steps: []string{
			"Wake the other PC. Devpit keeps its own PC awake, but it cannot keep that one awake.",
			"Devpit continues on its own when the other PC answers again.",
		},
		Action:    ActionRetry,
		Transient: true,
		Source:    "Windows sleep behaviour; detected by a port 445 check that stops answering",
	},
	{
		ID: "disk-full", Codes: []int{112},
		Title: "This disk is full",
		Why:   "There is not enough free space to finish the copy.",
		Steps: []string{
			"Free some space, or choose another disk.",
			"Then resume the copy. Finished files are skipped.",
		},
		Action: ActionChooseFolder,
		Source: winerror + ", ERROR_DISK_FULL",
	},
	{
		ID: "fat32", Codes: []int{223},
		Title: "This disk cannot hold a file this big",
		Why:   "The destination disk is formatted as FAT32. FAT32 cannot store one file over 4 GB.",
		Steps: []string{
			"Choose a disk formatted as NTFS or exFAT.",
			"Most USB sticks are FAT32. Format one as exFAT to fix this. That erases it.",
		},
		Action: ActionChooseFolder,
		Source: winerror + ", ERROR_FILE_TOO_LARGE",
	},
	{
		ID: "path-too-long", Codes: []int{206},
		Title: "A file path is too long",
		Why:   "Windows programs often stop at 260 characters in a path.",
		Steps: []string{
			"Choose a destination folder with a shorter path, for example D:\\Games.",
			"Then resume the copy.",
		},
		Action: ActionChooseFolder,
		Source: winerror + ", ERROR_FILENAME_EXCED_RANGE",
	},
	{
		ID: "in-use", Codes: []int{32, 33},
		Title:     "A file is in use",
		Why:       "Another program has the file open, on either PC.",
		Steps:     []string{"Close the program that uses it, then resume the copy."},
		Action:    ActionRetry,
		Transient: true,
		Source:    winerror + ", ERROR_SHARING_VIOLATION and ERROR_LOCK_VIOLATION",
	},
	{
		ID: "antivirus", Codes: nil,
		Title: "Antivirus may be slowing the copy",
		Why:   "The speed is far below what your network can do. Antivirus often checks every new file.",
		Steps: []string{
			"Wait. The copy is still working.",
			"If you trust the files, pause real-time scanning for the destination folder. Turn it back on after.",
		},
		Source: "Observed: many small files and low speed on a fast network",
	},
}

// Entries returns every known problem in table order. The docs page is
// generated from it. The slice is a copy.
func Entries() []Entry { return append([]Entry(nil), table...) }

// ByID returns the entry with the given ID.
func ByID(id string) (Entry, bool) {
	for _, e := range table {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Lookup returns the entry for a Win32 error number seen in phase.
//
// Two numbers read differently while copying. 53 and 67 during a copy mean
// the other PC went away, not that the address was typed wrong, and a
// network that drops is worth retrying.
func Lookup(code int, phase Phase) (Entry, bool) {
	if phase == PhaseCopy {
		switch code {
		case 53, 67, 1203:
			e, _ := ByID("share-removed")
			return e, true
		}
	}
	for _, e := range table {
		for _, c := range e.Codes {
			if c == code {
				return e, true
			}
		}
	}
	return Entry{}, false
}

// Explain finds the entry for err and the user it happened for.
//
// A wrong password for a name that looks like a Microsoft account e-mail is
// almost always the missing MicrosoftAccount\ prefix, so that entry wins in
// that one case. ok is false when nothing in the table matches, and the
// caller shows the error's own text.
func Explain(err error, phase Phase, user string) (Entry, bool) {
	code, ok := CodeOf(err)
	if !ok {
		return Entry{}, false
	}
	if (code == 1326 || code == 86) && LooksLikeMicrosoftAccount(user) {
		return ByID("microsoft-account")
	}
	return Lookup(code, phase)
}

// LooksLikeMicrosoftAccount reports whether user is an e-mail with no domain
// or MicrosoftAccount prefix, the shape that needs one.
func LooksLikeMicrosoftAccount(user string) bool {
	u := strings.TrimSpace(user)
	return strings.Contains(u, "@") && !strings.Contains(u, `\`)
}

// CodeOf digs a Win32 error number out of err: a syscall.Errno anywhere in
// the chain, which is what the Windows calls return.
func CodeOf(err error) (int, bool) {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return int(errno), true
	}
	return 0, false
}

// firstNumber finds the first whole number in a line of text.
var firstNumber = regexp.MustCompile(`\d+`)

// CodeFromText reads the error number out of what net.exe prints, such as
// "System error 53 has occurred." or "Systemfehler 53 aufgetreten.". The
// words change with the language, the number does not, so it takes the first
// number in the first line that has one.
func CodeFromText(text string) (int, bool) {
	for line := range strings.SplitSeq(text, "\n") {
		if m := firstNumber.FindString(line); m != "" {
			if n, err := strconv.Atoi(m); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}
