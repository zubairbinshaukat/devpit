//go:build windows

package winapi

import (
	"errors"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Account flags and levels from lmaccess.h.
const (
	// ufScript is required on every account created through NetUserAdd.
	ufScript = 0x0001
	// ufPasswdCantChange stops the temporary user changing its own password.
	ufPasswdCantChange = 0x0040
	// ufDontExpirePasswd keeps the password valid for as long as the share
	// exists, so a long copy never trips over a policy expiry.
	ufDontExpirePasswd = 0x10000
	// userPrivUser is an ordinary, non-admin account.
	userPrivUser = 1
	// infoLevelAcctExpires is USER_INFO_1017, the account expiry alone.
	infoLevelAcctExpires = 1017
)

// userInfo1 mirrors USER_INFO_1.
type userInfo1 struct {
	Name        *uint16
	Password    *uint16
	PasswordAge uint32
	Priv        uint32
	HomeDir     *uint16
	Comment     *uint16
	Flags       uint32
	ScriptPath  *uint16
}

// userInfo1017 mirrors USER_INFO_1017.
type userInfo1017 struct {
	AcctExpires uint32
}

// localGroupMembersInfo0 mirrors LOCALGROUP_MEMBERS_INFO_0.
type localGroupMembersInfo0 struct {
	Sid *windows.SID
}

// netapi is the Windows network management library.
var (
	netapi                     = windows.NewLazySystemDLL("netapi32.dll")
	procNetUserAdd             = netapi.NewProc("NetUserAdd")
	procNetUserDel             = netapi.NewProc("NetUserDel")
	procNetUserSetInfo         = netapi.NewProc("NetUserSetInfo")
	procNetLocalGroupAddMember = netapi.NewProc("NetLocalGroupAddMembers")
)

// netErr turns a NET_API_STATUS into an error, or nil for NERR_Success.
func netErr(code uintptr) error {
	if code == 0 {
		return nil
	}
	return syscall.Errno(code)
}

// createLocalUser implements [CreateLocalUser]: add the account, set its
// expiry, and put it in the built-in Users group by SID. Users is what grants
// the "access this computer from the network" right; the group is found by
// its well-known SID because its name is translated on every Windows
// language. On any failure after the account exists, the account is removed
// again so a half-made user is never left behind.
func createLocalUser(name, password string, expires time.Time) error {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(password)
	if err != nil {
		return err
	}
	info := userInfo1{
		Name: n, Password: p, Priv: userPrivUser,
		Flags: ufScript | ufPasswdCantChange | ufDontExpirePasswd,
	}
	var parm uint32
	code, _, _ := procNetUserAdd.Call(0, 1, uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&parm)))
	runtime.KeepAlive(&info)
	runtime.KeepAlive(p)
	if err = netErr(code); err != nil {
		return err
	}
	fail := func(err error) error {
		_ = deleteLocalUser(name)
		return err
	}

	exp := userInfo1017{AcctExpires: uint32(expires.Unix())} //nolint:gosec // an expiry within 2106 fits; NetUserSetInfo takes seconds as a DWORD
	code, _, _ = procNetUserSetInfo.Call(0, uintptr(unsafe.Pointer(n)), infoLevelAcctExpires,
		uintptr(unsafe.Pointer(&exp)), uintptr(unsafe.Pointer(&parm)))
	runtime.KeepAlive(&exp)
	if err = netErr(code); err != nil {
		return fail(err)
	}

	usersSID, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		return fail(err)
	}
	group, _, _, err := usersSID.LookupAccount("")
	if err != nil {
		return fail(err)
	}
	g, err := windows.UTF16PtrFromString(group)
	if err != nil {
		return fail(err)
	}
	userSID, _, _, err := windows.LookupSID("", name)
	if err != nil {
		return fail(err)
	}
	member := localGroupMembersInfo0{Sid: userSID}
	code, _, _ = procNetLocalGroupAddMember.Call(0, uintptr(unsafe.Pointer(g)), 0,
		uintptr(unsafe.Pointer(&member)), 1)
	runtime.KeepAlive(&member)
	runtime.KeepAlive(g)
	if err := groupAddErr(code); err != nil {
		return fail(err)
	}
	// The account is for signing in over the network only. Leaving it off
	// the sign-in screen stops a "devpit-ab12" tile appearing there for as
	// long as the share runs. It is a nicety: failing to do it is no reason
	// to fail the share.
	_ = hideFromSignIn(name, true)
	return nil
}

// errMemberInAlias is ERROR_MEMBER_IN_ALIAS: the account is already in the
// group.
const errMemberInAlias = 1378

// groupAddErr turns the result of NetLocalGroupAddMembers into an error.
// "Already a member" is success: depending on the Windows build, NetUserAdd
// with USER_PRIV_USER may have put the new account in Users already, and
// failing on that would delete the account again and fail every share.
func groupAddErr(code uintptr) error {
	if code == errMemberInAlias {
		return nil
	}
	return netErr(code)
}

// userListKey is where Windows looks for accounts to leave off the sign-in
// screen: a DWORD value named after the account, 0 to hide it.
const userListKey = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon\SpecialAccounts\UserList`

// hideFromSignIn adds or removes the account's value under [userListKey].
// Only the one value named after the account is touched; the key itself may
// hold the user's own entries and is never removed.
func hideFromSignIn(name string, hide bool) error {
	if !hide {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, userListKey, registry.SET_VALUE)
		if err != nil {
			return err
		}
		defer k.Close() //nolint:errcheck // nothing to do about a failed close
		if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, userListKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close() //nolint:errcheck // nothing to do about a failed close
	return k.SetDWordValue(name, 0)
}

// deleteLocalUser implements [DeleteLocalUser].
func deleteLocalUser(name string) error {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	_ = hideFromSignIn(name, false)
	code, _, _ := procNetUserDel.Call(0, uintptr(unsafe.Pointer(n)))
	runtime.KeepAlive(n)
	return netErr(code)
}

// localUserSID implements [LocalUserSID].
func localUserSID(name string) (string, error) {
	sid, _, _, err := windows.LookupSID("", name)
	if err != nil {
		return "", err
	}
	return sid.String(), nil
}
