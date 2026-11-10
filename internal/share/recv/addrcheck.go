package recv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
)

// Admin is the elevated worker as the receiving side sees it, for
// [Session.FreeAddress]. *elevate.Client is the real one.
type Admin interface {
	// Share runs one share operation. onLine receives progress lines.
	Share(ctx context.Context, req elevate.ShareRequest, onLine func(text string)) error
	// Close shuts the worker down.
	Close() error
}

// Launcher starts the elevated worker. It is where the one UAC prompt
// happens. A declined prompt is an *[elevate.DeclinedError].
type Launcher func(ctx context.Context) (Admin, error)

// RealLauncher starts this executable as the elevated worker, the same way
// the sharing side does.
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

// HostCheck says whether this PC can reach the other PC at its address, or
// holds that address itself. A PC that once ran its own Mobile Hotspot can
// keep the hotspot address (192.168.137.1) on one of its Wi-Fi Direct
// adapters; joining another PC's hotspot then gives two PCs the same
// address, and every connection to it stays on this PC.
type HostCheck struct {
	// Host is the address that was checked.
	Host string
	// Local is true when an adapter on this PC holds Host itself.
	Local bool
	// Holders names the adapters on this PC that hold Host, for the screen.
	Holders []string
	// Fixable is true when Local and every holder is a Wi-Fi Direct virtual
	// adapter, so [Session.FreeAddress] can take the address off.
	Fixable bool
	// Reachable is true when the other PC answers on TCP 445. It is never
	// set while Local: the answer would come from this PC itself.
	Reachable bool
	// Via is the adapter Windows routes Host through, "" when not known.
	Via string
}

// The HostCheck sentences. None names a speed.
const (
	reasonClashFixable = "This PC still holds the other PC's address %s on its own Wi-Fi Direct adapter, left over from its own Mobile Hotspot, so it is talking to itself. Devpit can take that address off (one administrator prompt); Windows puts it back the next time this PC turns on its own hotspot."
	reasonClashOther   = "This PC uses the other PC's address %s itself (on %s), so it cannot reach the other PC. Devpit only frees an address left on a Wi-Fi Direct adapter; change this one in the Windows network settings."
	reasonNoAnswer     = "The other PC does not answer at %s yet."
)

// Reason is one plain sentence about the check, "" when the other PC
// answers and nothing is wrong.
func (c HostCheck) Reason() string {
	switch {
	case c.Local && c.Fixable:
		return fmt.Sprintf(reasonClashFixable, c.Host)
	case c.Local:
		return fmt.Sprintf(reasonClashOther, c.Host, strings.Join(c.Holders, ", "))
	case !c.Reachable:
		return fmt.Sprintf(reasonNoAnswer, c.Host)
	}
	return ""
}

// Errors of the address fix.
var (
	// ErrNoAddressCheck means this session cannot read the PC's adapters.
	ErrNoAddressCheck = errors.New("this PC's network adapters cannot be read here")
	// ErrNotFixable means the address is on an adapter Devpit never changes.
	ErrNotFixable = errors.New("the address is on an adapter that is not a Wi-Fi Direct adapter; Devpit leaves it alone")
	// ErrStillHeld means the address is still on this PC after the fix:
	// Windows may have put it on another Wi-Fi Direct adapter.
	ErrStillHeld = errors.New("this PC still holds the other PC's address; Windows may have moved it to another adapter")
)

// probeTimeout bounds the 445 probe of [Session.CheckHost].
const probeTimeout = 3 * time.Second

// CheckHost checks host: whether this PC holds the address itself (and
// whether Devpit can free it), the adapter the route uses, and, when the
// address is not this PC's, whether the other PC answers on TCP 445. It only
// reads.
func (s *Session) CheckHost(ctx context.Context, host string) (HostCheck, error) {
	c, err := s.localCheck(host)
	if err != nil {
		return c, err
	}
	if !c.Local && s.deps.Dial != nil {
		c.Reachable = netstat.Reachable(ctx, s.deps.Dial, host, probeTimeout)
	}
	return c, nil
}

// localCheck is the part of [Session.CheckHost] that reads this PC's own
// adapters, with no packet sent.
func (s *Session) localCheck(host string) (HostCheck, error) {
	c := HostCheck{Host: host}
	if !privateIPv4(host) {
		return c, fmt.Errorf("checking %q: not a private IPv4 address", host)
	}
	if s.deps.Addresses == nil {
		return c, ErrNoAddressCheck
	}
	holders, err := s.deps.Addresses(host)
	if err != nil {
		return c, fmt.Errorf("reading this PC's addresses: %w", err)
	}
	c.Local = len(holders) > 0
	c.Fixable = c.Local
	for _, h := range holders {
		c.Holders = append(c.Holders, h.Alias)
		if !h.Virtual {
			c.Fixable = false
		}
		if h.Routes {
			c.Via = h.Alias
		}
	}
	if c.Via == "" && s.deps.Route != nil {
		if alias, _, rerr := s.deps.Route(host); rerr == nil {
			c.Via = alias
		}
	}
	return c, nil
}

// FreeAddress takes ip off this PC's Wi-Fi Direct virtual adapters, and off
// nothing else, through the elevated worker: one UAC prompt. It first checks
// that this PC holds ip and that every holder is such an adapter
// ([ErrNotFixable] otherwise, with no prompt), then launches the worker, sends
// the freeaddr operation, closes the worker and checks again. An address
// still held afterwards is [ErrStillHeld]. A PC that does not hold ip needs
// nothing and gets no prompt.
//
// Nothing has to be undone: Windows puts the address back by itself the next
// time this PC runs its own Mobile Hotspot.
func (s *Session) FreeAddress(ctx context.Context, ip string) error {
	c, err := s.localCheck(ip)
	if err != nil {
		return err
	}
	if !c.Local {
		return nil
	}
	if !c.Fixable {
		return ErrNotFixable
	}
	if s.deps.Admin == nil {
		return errors.New("the administrator helper is not available here")
	}
	admin, err := s.deps.Admin(ctx)
	if err != nil {
		return err
	}
	serr := admin.Share(ctx, elevate.ShareRequest{Op: elevate.ShareOpFreeAddr, FreeIP: ip}, nil)
	// The worker has nothing to undo for this operation; a failed close
	// only means it exits by itself when the pipe goes.
	_ = admin.Close()
	if serr != nil {
		return fmt.Errorf("freeing the address: %w", serr)
	}
	after, err := s.localCheck(ip)
	if err != nil {
		return err
	}
	if after.Local {
		return ErrStillHeld
	}
	return nil
}
