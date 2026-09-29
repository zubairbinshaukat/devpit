// Package recv is the receiving side of file sharing: list another PC's
// shares, sign in, check the size and the free space before starting, copy
// with progress and automatic retry, and carry on where a copy stopped.
//
// It has no Bubble Tea import. Robocopy, the network commands, the sign-in,
// the clock, the adapter counters and the disk are all injected, so the whole
// flow, including a network that drops and comes back, runs on fakes.
package recv

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
	"github.com/zubairbinshaukat/devpit/internal/share/robocopy"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// Deps is everything a [Session] touches.
type Deps struct {
	// Net runs `net view` and `net use`.
	Net netstat.Runner
	// Conn opens and closes sessions to shares.
	Conn netstat.Connector
	// Dial opens TCP connections, for the "is the other PC back" probe.
	Dial netstat.Dialer
	// Robo runs robocopy.
	Robo robocopy.Tool
	// Log returns the log source for a log file path.
	Log func(path string) robocopy.LogSource
	// FreeSpace reports the free bytes on the volume holding path.
	FreeSpace func(path string) (uint64, error)
	// FileSystem names the file system of the volume holding path.
	FileSystem func(path string) (string, error)
	// Counters returns the byte counters of the adapter that routes to host.
	// A failure means speed falls back to the copy's own file lines.
	Counters func(host string) (netstat.Counters, error)
	// KeepAwake stops the PC sleeping during a copy.
	KeepAwake func() (release func(), err error)
	// Now is the clock and Sleep the wait, both injected for tests.
	Now   func() time.Time
	Sleep func(context.Context, time.Duration)
	// Tick is how often speed is measured and progress is sent.
	Tick time.Duration
	// StateDir holds the resume record. LogDir holds robocopy's logs.
	StateDir, LogDir string
}

// Real returns the production dependencies, with the record in stateDir and
// robocopy's logs in logDir.
func Real(stateDir, logDir string) Deps {
	d := net.Dialer{Timeout: 3 * time.Second}
	return Deps{
		Net:  netstat.ExecRunner{},
		Conn: netstat.WinConnector{},
		Dial: d.DialContext,
		Robo: robocopy.Tool{Runner: robocopy.ExecRunner{}},
		Log:  func(p string) robocopy.LogSource { return robocopy.FileLog{Path: p} },
		FreeSpace: func(p string) (uint64, error) {
			ds, err := winapi.FreeSpace(p)
			return ds.FreeBytes, err
		},
		FileSystem: winapi.FileSystem,
		Counters: func(host string) (netstat.Counters, error) {
			c, _, err := netstat.AdapterCounters(host)
			return c, err
		},
		KeepAwake: winapi.KeepAwake,
		Now:       time.Now,
		Sleep:     netstat.SleepCtx,
		StateDir:  stateDir,
		LogDir:    logDir,
	}
}

// Session is one receiving flow. Its methods are called one after another by
// a screen, each from a command, so it holds no lock; [Session.Run] is the
// only long one.
type Session struct {
	deps Deps
	// Host is the other PC, once a list has succeeded.
	Host string
}

// New returns a session over deps, with defaults filled in for anything nil
// that a test does not care about.
func New(deps Deps) *Session {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Sleep == nil {
		deps.Sleep = netstat.SleepCtx
	}
	if deps.KeepAwake == nil {
		deps.KeepAwake = func() (func(), error) { return func() {}, nil }
	}
	if deps.Tick <= 0 {
		deps.Tick = time.Second
	}
	return &Session{deps: deps}
}

// List checks the address and lists the other PC's shared folders. It tries
// without signing in first, because a PC that shares openly needs no login,
// and returns the error as it came so the screen can ask for credentials when
// the number says the PC wants them.
func (s *Session) List(ctx context.Context, typed string) (host string, shares []netstat.Share, err error) {
	host, err = netstat.ParseHost(typed)
	if err != nil {
		return "", nil, err
	}
	shares, err = netstat.ListShares(ctx, s.deps.Net, host)
	if err != nil {
		return host, nil, err
	}
	s.Host = host
	return host, shares, nil
}

// SignInHost signs in to the other PC's IPC$ share with creds, which is what
// unlocks `net view` for a PC that wants a login, then lists its shares.
// Windows keeps one set of credentials per PC, so the same sign-in is what
// robocopy uses afterwards.
func (s *Session) SignInHost(ctx context.Context, host string, creds netstat.Credentials) ([]netstat.Share, error) {
	if err := netstat.SignIn(s.deps.Conn, host, "IPC$", creds); err != nil {
		return nil, err
	}
	shares, err := netstat.ListShares(ctx, s.deps.Net, host)
	if err != nil {
		return nil, err
	}
	s.Host = host
	return shares, nil
}

// SignInShare signs in to one share. If a session to the share is already
// there this is quick and harmless.
func (s *Session) SignInShare(host, share string, creds netstat.Credentials) error {
	return netstat.SignIn(s.deps.Conn, host, share, creds)
}

// DropConnections closes every open connection to host, for the "already
// signed in as someone else" fix.
func (s *Session) DropConnections(ctx context.Context, host string) (int, error) {
	return netstat.DropConnections(ctx, s.deps.Net, s.deps.Conn, host)
}

// Disconnect closes the connection to a share when a copy has ended.
func (s *Session) Disconnect(host, share string) {
	_ = s.deps.Conn.Disconnect(netstat.UNC(host, share))
	_ = s.deps.Conn.Disconnect(netstat.UNC(host, "IPC$"))
}

// staleLogAge is how old a robocopy log left by a run that crashed must be
// before the next run deletes it.
const staleLogAge = 7 * 24 * time.Hour

// newLogPath returns a fresh robocopy log path in the log directory. Each
// run deletes its own log once it has been read (see [Session.dropLog]); logs
// that a crashed run left behind are deleted here once they are a week old.
// A log lists every file with its full path, so a copy of a million files
// writes some hundreds of megabytes of it, and nothing ever read them again.
func (s *Session) newLogPath(kind string) (string, error) {
	if err := os.MkdirAll(s.deps.LogDir, 0o700); err != nil {
		return "", fmt.Errorf("creating the log folder: %w", err)
	}
	if old, err := filepath.Glob(filepath.Join(s.deps.LogDir, "robocopy-*.log")); err == nil {
		for _, p := range old {
			if st, err := os.Stat(p); err == nil && s.deps.Now().Sub(st.ModTime()) > staleLogAge {
				_ = os.Remove(p)
			}
		}
	}
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return filepath.Join(s.deps.LogDir, "robocopy-"+kind+"-"+hex.EncodeToString(b)+".log"), nil
}

// dropLog deletes a run's log once it has been read to the end.
func (s *Session) dropLog(path string) { _ = os.Remove(path) }

// LoadJob returns the saved copy, if any.
func (s *Session) LoadJob() (job.Job, bool, error) { return job.Load(s.deps.StateDir) }

// ClearJob forgets the saved copy.
func (s *Session) ClearJob() error { return job.Clear(s.deps.StateDir) }
