//go:build shots

package shots

import (
	"context"
	"math/rand/v2"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/share/host"
	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/share/robocopy"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/pathpicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/home"
	shareui "github.com/zubairbinshaukat/devpit/internal/ui/screens/share"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The demo network: the sharing PC is the one the network-ip shot shows
// (192.168.1.42 on Wi-Fi), sharing a games folder.
const (
	shareHostIP  = "192.168.1.42"
	shareName    = "Starfall"
	shareFolder  = `D:\Games\` + shareName
	shareDest    = `E:\Games\` + shareName
	shareGiB     = 80
	shareFiles   = 1184
	shareAdapter = "Wi-Fi"
)

// demoCard is the card a real share of shareFolder would show. The names and
// the password come from the same generators a real share uses, fed a fixed
// seed, so the picture shows the real format and never a real password.
func demoCard(t *testing.T) host.Card {
	t.Helper()
	src := rand.NewChaCha8([32]byte{'d', 'e', 'v', 'p', 'i', 't'})
	user, err := host.UserName(src)
	if err != nil {
		t.Fatal(err)
	}
	share, err := host.ShareName(src, shareName)
	if err != nil {
		t.Fatal(err)
	}
	password, err := host.Password(src, user)
	if err != nil {
		t.Fatal(err)
	}
	netUse, robo, cleanup := host.Commands(shareHostIP, share, user, password)
	return host.Card{
		IP: shareHostIP, Adapter: shareAdapter, Share: share, User: user, Password: password,
		Path: shareFolder, NetUse: netUse, Robocopy: robo, Cleanup: cleanup,
		Started: today,
	}
}

// demoHoster sets a share up at once and shows the demo card.
type demoHoster struct {
	card   host.Card
	mu     sync.Mutex
	active bool
}

func (h *demoHoster) Adapters(context.Context) ([]host.Adapter, error) {
	return []host.Adapter{{Name: shareAdapter, Index: 12, IP: net.ParseIP(shareHostIP), Best: true}}, nil
}

func (h *demoHoster) CategoryOf(context.Context, host.Adapter) (host.Category, error) {
	return host.CategoryPrivate, nil
}

func (h *demoHoster) Start(_ context.Context, _ host.Options, onStep func(host.Step)) (host.Card, error) {
	for _, k := range []string{"firewall", "login", "folder", "share"} {
		onStep(host.Step{Key: k})
	}
	h.mu.Lock()
	h.active = true
	h.mu.Unlock()
	return h.card, nil
}

func (h *demoHoster) Stop(context.Context) error {
	h.mu.Lock()
	h.active = false
	h.mu.Unlock()
	return nil
}

func (h *demoHoster) Active() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.active
}

// demoReceiver finds the demo PC, measures the copy and then reports one
// moment of a copy in progress, holding there until the session ends.
type demoReceiver struct {
	share string
	event recv.Event
	hold  chan struct{}
}

func (r *demoReceiver) List(context.Context, string) (string, []netstat.Share, error) {
	return shareHostIP, []netstat.Share{
		{Name: r.share, Type: "Disk"},
		{Name: "Users", Type: "Disk"},
	}, nil
}

func (r *demoReceiver) SignInHost(context.Context, string, netstat.Credentials) ([]netstat.Share, error) {
	_, s, err := r.List(context.Background(), shareHostIP)
	return s, err
}

func (r *demoReceiver) SignInShare(string, string, netstat.Credentials) error { return nil }

func (r *demoReceiver) DropConnections(context.Context, string) (int, error) { return 0, nil }

func (r *demoReceiver) Preflight(context.Context, string, string, string) (recv.Check, error) {
	return recv.Check{
		Plan:      robocopy.Plan{Files: shareFiles, Bytes: shareGiB << 30, SummaryFound: true},
		FreeBytes: 412 << 30, FileSystem: "NTFS",
	}, nil
}

func (r *demoReceiver) Start(h, s, u, d string, p robocopy.Plan) job.Job {
	return job.Job{Host: h, Share: s, User: u, Dest: d, TotalFiles: p.Files, TotalBytes: p.Bytes, State: job.StateRunning}
}

func (r *demoReceiver) Run(ctx context.Context, j job.Job, onEvent func(recv.Event)) (recv.Outcome, error) {
	onEvent(r.event)
	select {
	case <-r.hold:
	case <-ctx.Done():
	}
	return recv.Outcome{Paused: true, DoneBytes: r.event.DoneBytes}, nil
}

func (r *demoReceiver) Disconnect(string, string) {}

func (r *demoReceiver) ClearJob() error { return nil }

// copyingEvent is 47 of 80 GB done over a wired gigabit link, with the last
// few files robocopy finished and a network blip being retried.
func copyingEvent(share string) recv.Event {
	src := `\\` + shareHostIP + `\` + share + `\`
	return recv.Event{
		Phase:      recv.PhaseRetrying,
		DoneBytes:  47 << 30,
		TotalBytes: shareGiB << 30,
		DoneFiles:  702,
		TotalFiles: shareFiles,
		Speed:      108 * 1024 * 1024,
		ETA:        5*time.Minute + 12*time.Second,
		Recent: []string{
			src + `Content\Paks\world-03.pak`,
			src + `Content\Paks\world-04.pak`,
			src + `Content\Movies\intro-4k.bk2`,
			src + `Content\Audio\music-01.bank`,
		},
		Attempt: 1,
		Elapsed: 7*time.Minute + 26*time.Second,
		Note:    "The network hiccuped. Trying that file again.",
	}
}

// shareDeps are the Share Files dependencies over the demo engines.
func shareDeps(t *testing.T, s *session) shareui.Deps {
	card := demoCard(t)
	hold := make(chan struct{})
	s.onClose(func() { close(hold) })
	return shareui.Deps{
		NewHost: func() shareui.Hoster { return &demoHoster{card: card} },
		NewRecv: func() shareui.Receiver {
			return &demoReceiver{share: card.Share, event: copyingEvent(card.Share), hold: hold}
		},
		Leftover: func() (host.Manifest, bool) { return host.Manifest{}, false },
		SavedJob: func() (job.Job, bool) { return job.Job{}, false },
		CleanUp:  func(context.Context, host.Manifest) error { return nil },
	}
}

// shareAt opens Share Files and plays one of its flows.
func shareAt(flow func(t *testing.T, s *session), want string) scene {
	return func(t *testing.T, e entry) *session {
		var s *session
		s = newSession(t, e, demoConfig(), screens{
			home.SectionShare: func() uictx.Screen { return shareui.NewWith(shareDeps(t, s)) },
		})
		s.key(keyShare)
		s.waitFor("Copy from a shared folder")
		flow(t, s)
		s.waitFor(want)
		return s
	}
}

// shareHosting picks the folder to share; the demo engine sets it up at once.
func shareHosting(_ *testing.T, s *session) {
	s.key("enter")
	s.waitFor("Which folder do you want to share?")
	s.send(pathpicker.ChosenMsg{Path: shareFolder})
}

// shareCopying walks the receiving side with the sign-in from the demo card:
// the address, the share, the sign-in, the destination, the size check, and
// start.
func shareCopying(t *testing.T, s *session) {
	card := demoCard(t)
	s.key("down", "enter")
	s.waitFor("IP address")
	s.run("type:"+shareHostIP, "enter", "wait:"+card.Share, "enter")
	s.waitFor("Password")
	s.run("type:"+card.User, "tab", "type:"+card.Password, "enter")
	s.waitFor("Where should the files go?")
	s.send(pathpicker.ChosenMsg{Path: shareDest})
	s.waitFor("start copying")
	s.key("enter")
}
