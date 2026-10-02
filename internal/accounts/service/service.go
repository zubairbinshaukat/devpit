// Package service is the one entry point to Accounts for the command line
// and the app: it builds a ready-to-use engine (every adapter's undo
// handlers registered, crash recovery run, the shim manager wired) and puts
// every piece of behaviour the two share in one place, so `devpit claude`
// and the Accounts page say and do exactly the same things:
//
//   - Status and Overview: which account each tool uses in a folder, why,
//     who it is as far as Devpit knows, and the problems with their fixes;
//     no tool is run except Git's offline config read;
//   - Plan and Apply: the plain-words preview of a change and the change
//     itself, step by step, with Devpit's shims kept in line afterwards;
//   - Verify: expected against actual for every tool, honest about which
//     live checks are safe (LiveCheckRisk);
//   - Undo, Cleanup, the claude-acc import, adding an account, "just this
//     once", and the agent skill.
//
// Engine only: no Bubble Tea. Long operations return a channel of
// accounts.Event, like the adapters.
package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/claudeshare"
	"github.com/zubairbinshaukat/devpit/internal/accounts/launch"
	"github.com/zubairbinshaukat/devpit/internal/accounts/shims"
)

// Options builds a Service. Every zero field gets the real default; tests
// set temporary folders, a fake runner and a shim manager on a scratch
// folder with no PATH store.
type Options struct {
	// Paths are accounts.toml, the journal, the lock and the accounts
	// folder; accounts.DefaultPaths when zero (DEVPIT_CONFIG_DIR and
	// DEVPIT_ACCOUNTS_DIR are honoured).
	Paths accounts.Paths
	// Home is the person's home folder (~/.claude lives there).
	Home string
	// Runner runs the tools; adapters.ExecRunner skipping the shim folder
	// when nil.
	Runner adapters.Runner
	// Shims is the shim manager; shims.New when nil (DEVPIT_SHIM_DIR is
	// honoured).
	Shims *shims.Manager
	// Getenv reads the environment; os.Getenv when nil.
	Getenv func(string) string
	// LookPath finds a tool without running it; see adapters.Deps.
	LookPath func(string) (string, error)
	// Now is time.Now when nil.
	Now func() time.Time
	// Log gets one scrubbed line per command run, when set.
	Log func(string)
}

// Service is Accounts for one process. It is safe to use from one goroutine
// at a time per call; the adapters it holds cache what they probe.
type Service struct {
	// Deps are the adapters' dependencies.
	Deps adapters.Deps
	// Engine changes accounts; every adapter's and claudeshare's undo
	// handlers are registered on it.
	Engine *accounts.Engine
	// Shims manages the shim folder and the user PATH.
	Shims *shims.Manager
	// Recovered lists what crash recovery did when the Service was opened,
	// for the app to tell the person; RecoverErr is set when a change left
	// unfinished could not be finished or put back (a file changed by hand
	// since, usually).
	Recovered  []accounts.Recovered
	RecoverErr error

	mu       sync.Mutex
	adapters map[accounts.Tool]adapters.Adapter
}

// Handlers returns every undo handler an accounts change can need: the
// adapters' (Wrangler bindings) and claudeshare's (links, copies, merges).
// The engine's own file and folder kinds need none.
func Handlers(d adapters.Deps) map[accounts.EffectKind]accounts.EffectHandler {
	out := adapters.Handlers(d)
	for k, h := range claudeshare.Handlers() {
		out[k] = h
	}
	return out
}

// NewEngine returns an engine for d.Paths with every handler registered.
// Prefer Open, which also runs crash recovery.
func NewEngine(d adapters.Deps) *accounts.Engine {
	eng := accounts.NewEngine(d.Paths)
	eng.Handlers = Handlers(d)
	if d.Now != nil {
		eng.Now = d.Now
	}
	return eng
}

// Open builds the Service and runs crash recovery once, so a change a crash
// left half done is finished or put back before anything else happens. A
// recovery that cannot proceed is kept in RecoverErr, not returned: the
// person can still look at their accounts.
func Open(opt Options) (*Service, error) {
	p := opt.Paths
	if p.Store == "" {
		var err error
		if p, err = accounts.DefaultPaths(); err != nil {
			return nil, err
		}
	}
	home := opt.Home
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("accounts: locating your home folder: %w", err)
		}
		home = h
	}
	m := opt.Shims
	if m == nil {
		var err error
		if m, err = shims.New(); err != nil {
			return nil, err
		}
	}
	runner := opt.Runner
	if runner == nil {
		runner = adapters.ExecRunner{ShimDir: m.Dir, Log: opt.Log}
	}
	d := adapters.Deps{
		Runner: runner, Paths: p, Home: home, Now: opt.Now, Log: opt.Log,
		ShimDir: m.Dir, LookPath: opt.LookPath, Getenv: opt.Getenv,
	}
	s := &Service{Deps: d, Engine: NewEngine(d), Shims: m, adapters: map[accounts.Tool]adapters.Adapter{}}
	s.Recovered, s.RecoverErr = s.Engine.Recover()
	return s, nil
}

// Adapter returns the adapter for t, built once per Service so what it
// probes is asked once.
func (s *Service) Adapter(t accounts.Tool) (adapters.Adapter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.adapters[t]; ok {
		return a, nil
	}
	a, ok := adapters.For(s.Deps, t)
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", t)
	}
	s.adapters[t] = a
	return a, nil
}

// Load reads accounts.toml. warning is a sentence for the person when the
// file was unusable and moved aside.
func (s *Service) Load() (*accounts.Store, string, error) {
	res, err := s.Engine.Load()
	if err != nil {
		return nil, "", err
	}
	return res.Store, res.Warning, nil
}

func (s *Service) getenv(k string) string {
	if s.Deps.Getenv != nil {
		return s.Deps.Getenv(k)
	}
	return os.Getenv(k)
}

func (s *Service) now() time.Time {
	if s.Deps.Now != nil {
		return s.Deps.Now()
	}
	return time.Now()
}

// lookPath finds a tool on PATH outside the shim folder, without running it.
func (s *Service) lookPath(name string) (string, error) {
	if s.Deps.LookPath != nil {
		return s.Deps.LookPath(name)
	}
	return launch.Find(name, launch.FindOptions{Skip: []string{s.Shims.Dir}})
}

// DisplayPath is how a screen shows a path under the home folder: with ~.
func (s *Service) DisplayPath(p string) string {
	if s.Deps.Home != "" {
		if rel, err := filepath.Rel(s.Deps.Home, p); err == nil && rel != "." && !filepath.IsAbs(rel) && rel != ".." && !startsWithDotDot(rel) {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return p
}

func startsWithDotDot(rel string) bool {
	return len(rel) >= 3 && rel[:2] == ".." && (rel[2] == '\\' || rel[2] == '/')
}

// ErrUnknownTool is wrapped by ParseTool's error.
var ErrUnknownTool = errors.New("unknown tool")
