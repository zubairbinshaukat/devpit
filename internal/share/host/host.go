package host

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// Admin is the elevated worker as the sharing code sees it. *elevate.Client
// is the real one.
type Admin interface {
	// Share runs one share operation. onLine receives progress lines.
	Share(ctx context.Context, req elevate.ShareRequest, onLine func(text string)) error
	// Close shuts the worker down.
	Close() error
}

// Launcher starts the elevated worker. It is where the one UAC prompt
// happens. A declined prompt is an *[elevate.DeclinedError].
type Launcher func(ctx context.Context) (Admin, error)

// RealLauncher starts this executable as the elevated worker.
func RealLauncher(ctx context.Context) (Admin, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("finding devpit.exe: %w", err)
	}
	c, err := elevate.Launch(ctx, exe, elevate.Options{})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Deps is everything a [Manager] touches, so tests replace all of it.
type Deps struct {
	// Launch starts the elevated worker.
	Launch Launcher
	// Net reads adapters and network categories.
	Net NetInfo
	// Dir is the directory the manifest lives in.
	Dir string
	// Rand is the random source for names and the password. Nil means
	// crypto/rand, and nothing else is ever right in production.
	Rand io.Reader
	// Now is the clock.
	Now func() time.Time
	// KeepAwake stops the PC sleeping while the share is up, and returns
	// the function that lets it sleep again.
	KeepAwake func() (release func(), err error)
	// PID is this process's ID, written into the manifest.
	PID int
	// Getenv reads the environment, for the folder check. Nil means
	// os.Getenv.
	Getenv func(string) string
}

// withDefaults fills in what a caller left out.
func (d Deps) withDefaults() Deps {
	if d.Launch == nil {
		d.Launch = RealLauncher
	}
	if d.Net == nil {
		d.Net = SystemNet{}
	}
	if d.Rand == nil {
		d.Rand = rand.Reader
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.KeepAwake == nil {
		d.KeepAwake = winapi.KeepAwake
	}
	if d.PID == 0 {
		d.PID = os.Getpid()
	}
	if d.Getenv == nil {
		d.Getenv = os.Getenv
	}
	return d
}

// Options is what the user chose.
type Options struct {
	// Path is the folder to share.
	Path string
	// Adapter is the network connection to share on.
	Adapter Adapter
	// MakePrivate is the user's yes to switching a Public network to
	// Private. Windows blocks file sharing on a Public network, so without it
	// [Manager.Start] refuses a Public one with [ErrNeedPrivate].
	MakePrivate bool
}

// ErrNeedPrivate means the chosen network is Public and the user has not
// agreed to switch it. The screen asks, then starts again with MakePrivate.
var ErrNeedPrivate = errors.New("the network is Public and has to be Private to share")

// Step names one stage of setting up, for the progress list.
type Step struct {
	// Key is a stable identifier: network, firewall, login, folder, share.
	Key string
	// Label is what to show.
	Label string
}

// The steps, in order. The network step is skipped when the category is
// already right.
var (
	stepNetwork  = Step{"network", "Switching the network to Private"}
	stepFirewall = Step{"firewall", "Allowing file sharing in the firewall"}
	stepLogin    = Step{"login", "Creating a temporary login"}
	stepFolder   = Step{"folder", "Letting that login read the folder"}
	stepShare    = Step{"share", "Sharing the folder read-only"}
)

// Card is what the sharing PC shows: everything the other PC needs.
type Card struct {
	// IP is the address to type on the other PC.
	IP string
	// Adapter is the connection name it belongs to.
	Adapter string
	// Share is the share name.
	Share string
	// User and Password are the temporary login. The password lives in
	// memory only.
	User, Password string
	// Path is the folder being shared.
	Path string
	// NetUse, Robocopy and Cleanup are the commands for a PC without Devpit.
	NetUse, Robocopy, Cleanup string
	// Started is when sharing began.
	Started time.Time
	// SwitchedNetwork says Devpit changed the network to Private and will
	// change it back.
	SwitchedNetwork bool
}

// Commands returns the three commands for a PC without Devpit: sign in, copy,
// and sign out. The password is an alphanumeric word, so none of the lines
// needs quoting around it. The copy goes into a new folder, named after the
// share, where the terminal is open: that folder exists on every PC and works
// the same in cmd and PowerShell, which a drive letter or %USERPROFILE% does
// not. /XO keeps a newer file on that PC instead of overwriting it, the same
// rule Devpit's own copy follows.
func Commands(ip, share, user, password string) (netUse, robocopy, cleanup string) {
	unc := `\\` + ip + `\` + share
	netUse = fmt.Sprintf(`net use %s /user:%s %s`, unc, user, password)
	robocopy = fmt.Sprintf(`robocopy %s ".\%s" /E /Z /MT:16 /R:3 /W:5 /XO`, unc, share)
	cleanup = fmt.Sprintf(`net use %s /delete`, unc)
	return netUse, robocopy, cleanup
}

// Manager owns one share from setup to teardown. It is safe for concurrent
// use: Stop may be called from a signal handler while a screen reads the
// card.
type Manager struct {
	deps Deps

	// stopMu makes Stop one at a time: a signal handler and the screen may
	// both ask, and the second must find the work already done.
	stopMu sync.Mutex

	mu      sync.Mutex
	admin   Admin
	man     Manifest
	card    Card
	up      bool
	release func()
}

// NewManager returns a manager over deps and registers it, so a quit or a
// signal can find and stop it (see [ShutdownAll]).
func NewManager(deps Deps) *Manager {
	m := &Manager{deps: deps.withDefaults()}
	register(m)
	return m
}

// Active reports whether a share is up, or half set up.
func (m *Manager) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.admin != nil
}

// Card returns the card of the running share.
func (m *Manager) Card() (Card, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.card, m.up
}

// Adapters lists the adapters to offer.
func (m *Manager) Adapters(ctx context.Context) ([]Adapter, error) { return m.deps.Net.Adapters(ctx) }

// CategoryOf returns the network category of an adapter, so the screen can
// ask before switching a Public network.
func (m *Manager) CategoryOf(ctx context.Context, a Adapter) (Category, error) {
	return m.deps.Net.CategoryOf(ctx, a.Index)
}

// Start sets everything up. It launches the worker (the one UAC prompt),
// writes the manifest, and runs the operations in order, telling onStep before
// each one. Every effect is recorded in the manifest before the next begins.
// If anything fails, whatever was done is undone and the error returned; if
// the user declines the prompt nothing was done at all.
func (m *Manager) Start(ctx context.Context, opts Options, onStep func(Step)) (Card, error) {
	m.mu.Lock()
	if m.admin != nil {
		m.mu.Unlock()
		return Card{}, errors.New("a share is already running")
	}
	m.mu.Unlock()

	// Refuse a folder that must never be shared before anything happens,
	// even the admin prompt.
	if err := CheckFolder(opts.Path, m.deps.Getenv); err != nil {
		return Card{}, err
	}

	cat, err := m.deps.Net.CategoryOf(ctx, opts.Adapter.Index)
	if err != nil {
		return Card{}, err
	}
	switchNet := cat == CategoryPublic
	if switchNet && !opts.MakePrivate {
		return Card{}, ErrNeedPrivate
	}

	user, err := UserName(m.deps.Rand)
	if err != nil {
		return Card{}, err
	}
	password, err := Password(m.deps.Rand, user)
	if err != nil {
		return Card{}, err
	}
	share, err := ShareName(m.deps.Rand, lastElement(opts.Path))
	if err != nil {
		return Card{}, err
	}

	admin, err := m.deps.Launch(ctx)
	if err != nil {
		return Card{}, err
	}
	man := Manifest{
		PID: m.deps.PID, StartedAt: m.deps.Now(),
		User: user, Path: opts.Path, Share: share,
	}
	m.mu.Lock()
	m.admin, m.man = admin, man
	m.mu.Unlock()

	fail := func(err error) (Card, error) {
		// The setup is undone with a fresh context: the caller's may be the
		// very thing that was cancelled.
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if serr := m.Stop(cctx); serr != nil {
			return Card{}, errors.Join(err, fmt.Errorf("and cleaning up failed: %w", serr))
		}
		return Card{}, err
	}

	// The manifest goes to disk before anything is created, naming what is
	// about to be, so a crash at any point leaves a record that covers it.
	if err = m.save(); err != nil {
		return fail(err)
	}
	step := func(s Step) {
		if onStep != nil {
			onStep(s)
		}
	}

	if switchNet {
		step(stepNetwork)
		req := elevate.ShareRequest{
			Op: elevate.ShareOpProfile, Index: opts.Adapter.Index,
			Category: string(CategoryPrivate), Original: string(cat),
		}
		m.update(func(mm *Manifest) { mm.ProfileIndex, mm.ProfileOriginal = req.Index, req.Original })
		if err = m.saveOrFail(); err != nil {
			return fail(err)
		}
		if err = admin.Share(ctx, req, nil); err != nil {
			return fail(fmt.Errorf("switching the network to Private: %w", err))
		}
	}

	// recordRules notes every firewall rule the worker says it turned on
	// ("FW=<name>"), so Stop, and the next launch after a crash, turn it off
	// again. Both the firewall step and the share step report them: on 24H2,
	// creating a share can turn on FPS-SMB-In-TCP-V2 by itself.
	recordRules := func(l string) {
		if rule, ok := strings.CutPrefix(l, "FW="); ok {
			m.update(func(mm *Manifest) {
				if !slices.Contains(mm.Rules, rule) {
					mm.Rules = append(mm.Rules, rule)
				}
			})
		}
	}

	step(stepFirewall)
	if err = admin.Share(ctx, elevate.ShareRequest{Op: elevate.ShareOpFirewall}, recordRules); err != nil {
		_ = m.saveOrFail()
		return fail(fmt.Errorf("allowing file sharing in the firewall: %w", err))
	}
	if err = m.saveOrFail(); err != nil {
		return fail(err)
	}

	step(stepLogin)
	err = admin.Share(ctx, elevate.ShareRequest{Op: elevate.ShareOpAccount, User: user, Password: password}, func(l string) {
		if sid, ok := strings.CutPrefix(l, "SID="); ok {
			m.update(func(mm *Manifest) { mm.SID = sid })
		}
	})
	if err != nil {
		return fail(fmt.Errorf("creating the temporary login: %w", err))
	}
	if err = m.saveOrFail(); err != nil {
		return fail(err)
	}

	step(stepFolder)
	// Recorded before the permission is written, so a crash in between still
	// has it cleaned up; not before, because until the account exists there
	// is no permission to remove and icacls fails on the unknown name.
	m.update(func(mm *Manifest) { mm.Granted = true })
	if err = m.saveOrFail(); err != nil {
		return fail(err)
	}
	if err = admin.Share(ctx, elevate.ShareRequest{Op: elevate.ShareOpGrant, User: user, Path: opts.Path}, nil); err != nil {
		return fail(fmt.Errorf("giving the login read access to the folder: %w", err))
	}

	step(stepShare)
	err = admin.Share(ctx, elevate.ShareRequest{Op: elevate.ShareOpShare, User: user, Path: opts.Path, Name: share}, recordRules)
	// Saved whether or not the share worked: a rule it turned on before
	// failing must still be turned off.
	if serr := m.saveOrFail(); err == nil {
		err = serr
	}
	if err != nil {
		return fail(fmt.Errorf("sharing the folder: %w", err))
	}

	ip := opts.Adapter.IP.String()
	netUse, robo, cleanup := Commands(ip, share, user, password)
	card := Card{
		IP: ip, Adapter: opts.Adapter.Name, Share: share, User: user, Password: password,
		Path: opts.Path, NetUse: netUse, Robocopy: robo, Cleanup: cleanup,
		Started: man.StartedAt, SwitchedNetwork: switchNet,
	}
	// A PC that goes to sleep stops sharing, so keep it awake for as long as
	// the share is up. Failing to is not a reason to fail the share.
	rel, aerr := m.deps.KeepAwake()
	if aerr != nil {
		rel = func() {}
	}
	m.mu.Lock()
	m.card, m.up, m.release = card, true, rel
	m.mu.Unlock()
	return card, nil
}

// update changes the in-memory manifest under the lock.
func (m *Manager) update(f func(*Manifest)) {
	m.mu.Lock()
	f(&m.man)
	m.mu.Unlock()
}

// save writes the current manifest to disk.
func (m *Manager) save() error {
	m.mu.Lock()
	man := m.man
	m.mu.Unlock()
	if err := saveManifest(m.deps.Dir, man); err != nil {
		return fmt.Errorf("saving the cleanup record: %w", err)
	}
	return nil
}

// saveOrFail is save with a name that reads well at the call sites.
func (m *Manager) saveOrFail() error { return m.save() }

// Stop takes everything down: it asks the worker to undo each step, then
// deletes the manifest and closes the worker. It is safe to call twice and
// from any goroutine. If a step cannot be undone the manifest stays, so the
// next launch offers to finish, and the error names what is left.
func (m *Manager) Stop(ctx context.Context) error {
	m.stopMu.Lock()
	defer m.stopMu.Unlock()
	m.mu.Lock()
	admin := m.admin
	m.mu.Unlock()
	if admin == nil {
		return nil
	}
	if err := admin.Share(ctx, elevate.ShareRequest{Op: elevate.ShareOpStop}, nil); err != nil {
		return fmt.Errorf("stopping the share: %w", err)
	}
	m.mu.Lock()
	man := m.man
	m.mu.Unlock()
	if err := removeManifest(m.deps.Dir, man); err != nil {
		return fmt.Errorf("removing the cleanup record: %w", err)
	}
	_ = admin.Close()
	m.mu.Lock()
	if m.release != nil {
		m.release()
	}
	m.admin, m.release, m.up, m.card = nil, nil, false, Card{}
	m.mu.Unlock()
	return nil
}

// lastElement returns the last part of a Windows path, whatever separator it
// uses, without touching the disk. path/filepath would use the separator of
// the system the code runs on, which is not the path's.
func lastElement(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return strings.TrimSuffix(p, ":")
}
