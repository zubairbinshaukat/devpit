// Package job remembers a copy so that closing Devpit, or losing the Wi-Fi,
// does not lose it. The record holds where the files come from, where they
// go, who signed in and how far the copy got. It never holds the password.
//
// Why no password. A password saved on disk is a password anyone with that
// file can read, and the sharing PC's temporary login is meant to last one
// session. Resume needs it only if Windows has forgotten the sign-in, and
// Devpit closes its own connection only when a copy ends. Closing Devpit in
// the middle leaves Windows' temporary connection open until sign-out, and
// that is enough to carry on. When it is gone, Resume asks for the password
// again, with the user name already filled in. Robocopy needs no state of its
// own to resume: finished files are skipped because their size and time match,
// and a half-copied file continues thanks to /Z.
package job

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/share/store"
)

// fileName is the record's name inside the share directory.
const fileName = "share-recv.json"

// version is bumped when the record's shape changes.
const version = 1

// State is where a copy stands.
type State string

// The states a saved copy can be in.
const (
	// StateRunning is a copy that was going when the record was last
	// written. Found at launch, it means Devpit was closed or crashed.
	StateRunning State = "running"
	// StatePaused is a copy the user stopped, or one waiting for the other
	// PC to come back.
	StatePaused State = "paused"
	// StateFailed is a copy that ended with files that did not copy.
	StateFailed State = "failed"
	// StateDone is a finished copy. It is kept until the user has seen it.
	StateDone State = "done"
)

// Job is one copy.
type Job struct {
	// Version is the record's format.
	Version int `json:"version"`
	// Host is the other PC's address as typed.
	Host string `json:"host"`
	// Share is the shared folder's name.
	Share string `json:"share"`
	// User is the account that signed in. The password is not saved.
	User string `json:"user"`
	// Dest is the folder the files go to.
	Dest string `json:"dest"`
	// TotalFiles and TotalBytes are what the first dry run found.
	TotalFiles int64 `json:"total_files"`
	TotalBytes int64 `json:"total_bytes"`
	// DoneFiles and DoneBytes are what earlier runs of this copy finished.
	DoneFiles int64 `json:"done_files"`
	DoneBytes int64 `json:"done_bytes"`
	// State is where the copy stands.
	State State `json:"state"`
	// LastError says why the copy stopped, in plain words, when it did.
	LastError string `json:"last_error,omitempty"`
	// Failed are the files that did not copy, when the copy ended.
	Failed []string `json:"failed,omitempty"`
	// Started and Updated are the times of the first and latest write.
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
}

// Remaining returns the bytes still to copy.
func (j Job) Remaining() int64 { return max(0, j.TotalBytes-j.DoneBytes) }

// Resumable reports whether the record is a copy that can be carried on.
func (j Job) Resumable() bool {
	return j.State != StateDone && j.Host != "" && j.Share != "" && j.Dest != ""
}

// path is the record's file under dir.
func path(dir string) string { return filepath.Join(dir, fileName) }

// Save writes the record atomically.
func Save(dir string, j Job) error {
	j.Version = version
	return store.WriteJSON(path(dir), j)
}

// Load reads the record, and reports false when there is none. A damaged
// record is an error.
func Load(dir string) (Job, bool, error) {
	var j Job
	err := store.ReadJSON(path(dir), &j)
	if errors.Is(err, os.ErrNotExist) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	return j, true, nil
}

// Clear deletes the record.
func Clear(dir string) error { return store.Remove(path(dir)) }
