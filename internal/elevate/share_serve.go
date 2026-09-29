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

// handleShare runs one share operation and answers with a done event. The
// operation's progress lines go back as line events on the "info" stream.
func handleShare(ctx context.Context, lw *lineWriter, ss *shareSession, req Request) {
	if req.Share == nil {
		_ = lw.writeJSON(Event{ID: req.ID, Event: EventDone, ExitCode: -1, Err: "refused: share job without a share request"})
		return
	}
	emit := func(stream, text string) {
		_ = lw.writeJSON(Event{ID: req.ID, Event: EventLine, Stream: stream, Text: text})
	}
	done := Event{ID: req.ID, Event: EventDone}
	if err := ss.run(ctx, *req.Share, emit); err != nil {
		done.ExitCode = -1
		done.Err = err.Error()
	}
	_ = lw.writeJSON(done)
}
