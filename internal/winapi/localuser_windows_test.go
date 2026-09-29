//go:build windows

package winapi

import (
	"errors"
	"syscall"
	"testing"
)

// TestAddingToUsersWhenAlreadyAMemberIsSuccess: NetUserAdd with
// USER_PRIV_USER may already have put the account in Users. Treating
// ERROR_MEMBER_IN_ALIAS as a failure deleted the fresh account and failed the
// share on such a build.
func TestAddingToUsersWhenAlreadyAMemberIsSuccess(t *testing.T) {
	if err := groupAddErr(0); err != nil {
		t.Errorf("success = %v", err)
	}
	if err := groupAddErr(errMemberInAlias); err != nil {
		t.Errorf("already a member = %v, want success", err)
	}
	var errno syscall.Errno
	if err := groupAddErr(5); !errors.As(err, &errno) || errno != 5 {
		t.Errorf("access denied = %v, want Errno 5", err)
	}
}
