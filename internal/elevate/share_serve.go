package elevate

import (
	"context"
	"errors"
	"syscall"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// nerrUserNotFound is NERR_UserNotFound: deleting an account that is already
// gone, which for a cleanup means the job is done.
const nerrUserNotFound = 2221

// isMissingUser reports whether err says the account does not exist.
func isMissingUser(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == nerrUserNotFound
}

// CreateUser implements [UserManager] with the Windows account functions.
func (DefaultExecutor) CreateUser(name, password string, expires time.Time) error {
	return winapi.CreateLocalUser(name, password, expires)
}

// DeleteUser implements [UserManager].
func (DefaultExecutor) DeleteUser(name string) error { return winapi.DeleteLocalUser(name) }

// UserSID implements [UserManager].
func (DefaultExecutor) UserSID(name string) (string, error) { return winapi.LocalUserSID(name) }

// SIDAccount implements [sidResolver].
func (DefaultExecutor) SIDAccount(sid string) (string, bool, error) { return lookupSIDAccount(sid) }

// handleShare runs one share operation under ctx (cancelled by a
// [KindCancel] for it, by its time limit, or by shutdown). The operation's
// progress lines go back as line events on the "info" stream; the done event
// is returned.
func handleShare(ctx context.Context, lw *lineWriter, ss *shareSession, req Request) Event {
	if req.Share == nil {
		return Event{ID: req.ID, Event: EventDone, ExitCode: -1, Err: "refused: share job without a share request"}
	}
	emit := func(stream, text string) {
		_ = lw.writeJSON(Event{ID: req.ID, Event: EventLine, Stream: stream, Text: text})
	}
	done := Event{ID: req.ID, Event: EventDone}
	if err := ss.run(ctx, *req.Share, emit); err != nil {
		done.ExitCode = -1
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			done.Err = "share " + string(req.Share.Op) + " timed out: " + err.Error()
		case ctx.Err() != nil:
			done.Err = "cancelled"
		default:
			done.Err = err.Error()
		}
	}
	return done
}
