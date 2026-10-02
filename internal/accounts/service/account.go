package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/importer"
	"github.com/zubairbinshaukat/devpit/internal/accounts/launch"
)

// placeholderPrefix starts the temporary name a sign-in runs under before
// the person names the account (the name is asked after the sign-in, from
// the email it found).
const placeholderPrefix = "new-"

// NameBeforeSignIn reports whether a tool needs the account's final name
// before its sign-in starts: Wrangler names its profile at `wrangler auth
// create <name>`, and Git has no sign-in at all.
func NameBeforeSignIn(t accounts.Tool) bool {
	return t == accounts.ToolCloudflare || t == accounts.ToolGit
}

// PlaceholderName is a temporary name for a sign-in that is named after.
func PlaceholderName() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return placeholderPrefix + hex.EncodeToString(b[:])
}

// SignIn runs the tool's own sign-in for a new account and streams its
// steps; the Final event carries the Account to save, not saved yet. name
// may be a PlaceholderName (see NameBeforeSignIn). A sign-in that does not
// finish leaves nothing behind.
func (s *Service) SignIn(ctx context.Context, t accounts.Tool, req adapters.LoginRequest) (<-chan accounts.Event, error) {
	a, err := s.Adapter(t)
	if err != nil {
		return nil, err
	}
	if caps := a.Capabilities(ctx); !caps.AddAccount {
		return nil, fmt.Errorf("%s: %w", strings.TrimRight(caps.Why, "."), accounts.ErrNotSupported)
	}
	if req.Store == nil {
		st, _, err := s.Load()
		if err != nil {
			return nil, err
		}
		req.Store = st
	}
	return a.Login(ctx, req)
}

// SuggestName is the name offered for a new account, from its email.
func (s *Service) SuggestName(t accounts.Tool, email string) string {
	st, _, err := s.Load()
	if err != nil {
		st = accounts.NewStore()
	}
	return accounts.SuggestName(email, st.Names(t))
}

// SaveAccount saves an account a sign-in returned under the name the person
// chose. If the sign-in ran under a placeholder, its fresh folder is renamed
// to match; if that is not possible the folder keeps its name and only the
// account is named. One undoable change; the shims follow.
func (s *Service) SaveAccount(acct accounts.Account, name string) (accounts.Account, accounts.Entry, error) {
	st, _, err := s.Load()
	if err != nil {
		return acct, accounts.Entry{}, err
	}
	if err = st.CheckNewName(acct.Tool, name); err != nil {
		return acct, accounts.Entry{}, err
	}
	if acct.Name != name && acct.Dir != "" && strings.HasPrefix(acct.Name, placeholderPrefix) &&
		accounts.SameFolder(acct.Dir, s.Deps.Paths.AccountDir(acct.Tool, acct.Name)) {
		to := s.Deps.Paths.AccountDir(acct.Tool, name)
		if _, lerr := os.Lstat(to); errors.Is(lerr, os.ErrNotExist) {
			if os.Rename(acct.Dir, to) == nil {
				acct.Dir = to
			}
		}
	}
	acct.Name = name
	ent, err := s.Engine.AddAccount(acct)
	if err != nil {
		return acct, ent, err
	}
	s.syncShims(nil)
	return acct, ent, nil
}

// DetectClaudeAcc looks for claude-acc's accounts and links, read only. ok
// is false when claude-acc is not on this PC.
func (s *Service) DetectClaudeAcc(ctx context.Context) (importer.Found, bool, error) {
	st, _, err := s.Load()
	if err != nil {
		return importer.Found{}, false, err
	}
	f, err := importer.Detect(ctx, importer.Deps{Home: s.Deps.Home, Getenv: s.Deps.Getenv}, st)
	if err != nil {
		return importer.Found{}, false, err
	}
	return f, f.ClaudeAcc != nil, nil
}

// PlanImport previews bringing what was found into Devpit. It changes
// nothing; Plan.Lines says it in plain words.
func (s *Service) PlanImport(f importer.Found, opt importer.Options) (importer.Plan, error) {
	st, _, err := s.Load()
	if err != nil {
		return importer.Plan{}, err
	}
	if opt.Now.IsZero() {
		opt.Now = s.now()
	}
	return importer.PlanImport(st, f, opt)
}

// ApplyImport makes a previewed import as one undoable change, then brings
// the shims in line.
func (s *Service) ApplyImport(p importer.Plan, emit func(accounts.Event)) (accounts.Entry, error) {
	ent, err := importer.ApplyImport(s.Engine, p)
	if err != nil {
		return ent, err
	}
	if emit == nil {
		emit = func(accounts.Event) {}
	}
	s.syncShims(emit)
	return ent, nil
}

// OnceCommand is a command to start with an account forced, just this once.
type OnceCommand struct {
	// Path is the program found (the real tool, outside the shim folder,
	// when the command starts with the tool), Args its arguments with the
	// account's own arguments put in, Env the whole environment.
	Path string
	Args []string
	Env  []string
	Find launch.FindOptions
}

// PrepareOnce works out how to run argv with tool's account name forced for
// that one command and everything it starts; nothing is saved. A tool whose
// account is a command-line option (Vercel, Firebase, Cloudflare) needs the
// command to start with the tool itself (or npx and the tool). The token a
// GitHub account needs is fetched now, from gh, and only ever goes into
// Env: never print Env.
func (s *Service) PrepareOnce(ctx context.Context, tool accounts.Tool, name string, argv []string) (OnceCommand, error) {
	if len(argv) == 0 {
		return OnceCommand{}, errors.New("give the command to run after --")
	}
	a, err := s.Adapter(tool)
	if err != nil {
		return OnceCommand{}, err
	}
	if caps := a.Capabilities(ctx); !caps.JustOnce {
		why := caps.Why
		if why == "" {
			why = tool.DisplayName() + " has no \"just this once\""
		}
		return OnceCommand{}, fmt.Errorf("%s: %w", strings.TrimRight(why, "."), accounts.ErrNotSupported)
	}
	st, _, err := s.Load()
	if err != nil {
		return OnceCommand{}, err
	}
	acct, err := st.FindAccount(tool, name)
	if err != nil {
		return OnceCommand{}, err
	}
	l, err := adapters.LaunchLive(ctx, s.Deps.Runner, acct)
	if err != nil {
		return OnceCommand{}, err
	}
	find := launch.FindOptions{}
	if s.Shims != nil {
		find.Skip = []string{s.Shims.Dir}
	}
	prog := strings.ToLower(strings.TrimSuffix(filepath.Base(argv[0]), filepath.Ext(argv[0])))
	args := append([]string(nil), argv[1:]...)
	switch {
	case prog == tool.Binary():
		args = append(append([]string(nil), l.Args...), args...)
	case prog == "npx" && len(args) > 0 && strings.EqualFold(args[0], tool.Binary()):
		args = append(append([]string{args[0]}, l.Args...), args[1:]...)
	case len(l.Args) > 0:
		return OnceCommand{}, fmt.Errorf("%s takes the account from its own command-line option (%s), so the command must start with %s, for example: devpit %s run %s -- %s …",
			tool.DisplayName(), l.Args[0], tool.Binary(), tool, acct.Name, tool.Binary())
	}
	path, err := launch.Find(argv[0], find)
	if err != nil && s.Deps.LookPath != nil {
		// Options.LookPath stands in for PATH (tests, and a caller that
		// knows where the tool is).
		path, err = s.Deps.LookPath(argv[0])
	}
	if err != nil {
		return OnceCommand{}, fmt.Errorf("%s was not found on PATH", argv[0])
	}
	env := launch.MergeEnv(os.Environ(), append(append([]string(nil), l.Env...), launch.EnvOnce+"="+string(tool)+":"+acct.Name), l.Unset)
	return OnceCommand{Path: path, Args: args, Env: env, Find: find}, nil
}

// RunOnce runs a prepared command on this console and returns its exit
// code.
func RunOnce(c OnceCommand) (int, error) {
	return launch.Run(launch.Spec{Path: c.Path, Args: c.Args, Env: c.Env, Find: c.Find})
}
