package netstat_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseHost(t *testing.T) {
	tests := []struct {
		in, want string
		bad      bool
	}{
		{"192.168.1.5", "192.168.1.5", false},
		{"  192.168.1.5  ", "192.168.1.5", false},
		{`\\192.168.1.5`, "192.168.1.5", false},
		{`\\192.168.1.5\Games`, "192.168.1.5", false},
		{"MY-PC", "MY-PC", false},
		{"my-pc.local", "my-pc.local", false},
		{"fd00::1", "fd00--1.ipv6-literal.net", false},
		{"", "", true},
		{"a b", "", true},
		{"192.168.1.5 & calc", "", true},
		{"host;rm", "", true},
		{"-bad", "", true},
		{"bad-", "", true},
		{"fe80::1", "", true},
		{"$(x)", "", true},
	}
	for _, tt := range tests {
		got, err := netstat.ParseHost(tt.in)
		if tt.bad {
			if err == nil || !errors.Is(err, netstat.ErrBadHost) {
				t.Errorf("ParseHost(%q) = %q, %v; want ErrBadHost", tt.in, got, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("ParseHost(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestUNC(t *testing.T) {
	if got := netstat.UNC("h", "s"); got != `\\h\s` {
		t.Error(got)
	}
	if got := netstat.UNC("h", ""); got != `\\h` {
		t.Error(got)
	}
}

func TestParseNetViewAcrossLanguages(t *testing.T) {
	want := map[string][]string{
		"en": {"Games", "Movies", "Printer1"},
		"de": {"Spiele", "Filme", "Drucker1"},
		"fr": {"Jeux", "Vidéos", "Imprimante1"},
		"ja": {"ゲーム", "動画", "プリンター1"},
		"tr": {"Oyunlar", "Filmler", "Yazıcı1"},
	}
	for loc, names := range want {
		t.Run(loc, func(t *testing.T) {
			shares := netstat.ParseNetView(fixture(t, "netview_"+loc+".txt"))
			var got []string
			for _, s := range shares {
				got = append(got, s.Name)
			}
			if !reflect.DeepEqual(got, names) {
				t.Fatalf("names = %q, want %q (the hidden C$ share must be dropped, the footer must not be a share)", got, names)
			}
			if shares[0].Type == "" || !strings.Contains(shares[0].Comment, loc) {
				t.Errorf("first share = %+v, want its type and its comment", shares[0])
			}
			if shares[1].Comment != "" {
				t.Errorf("a share with no comment has %q", shares[1].Comment)
			}
		})
	}
}

func TestParseNetViewHandlesOddOutput(t *testing.T) {
	for name, in := range map[string]string{
		"empty":         "",
		"no table":      "System error 53 has occurred.\n\nThe network path was not found.\n",
		"table no rows": "Shared resources at \\\\h\n\nShare name  Type\n\n----------------------------\nThe command completed successfully.\n",
	} {
		if got := netstat.ParseNetView(in); len(got) != 0 {
			t.Errorf("%s: got %v, want none", name, got)
		}
	}
	// A last row is kept when there is no footer to drop.
	got := netstat.ParseNetView("----------\nGames        Disk         hi\n")
	if len(got) != 1 || got[0].Name != "Games" {
		t.Errorf("a table with no footer = %v", got)
	}
}

func TestParseConnectionsFindsUNCWordsWhateverTheLanguage(t *testing.T) {
	got := netstat.ParseConnections(fixture(t, "netuse_list.txt"))
	want := []string{`\\192.168.1.5\IPC$`, `\\192.168.1.5\Games`, `\\192.168.1.9\Other`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("connections = %q, want %q", got, want)
	}
}

// fakeRunner returns canned output for a command line.
type fakeRunner struct {
	out   map[string]string
	exit  map[string]int
	calls []string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, int, error) {
	key := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, key)
	return f.out[key], f.exit[key], nil
}

func TestListSharesTurnsANetErrorIntoItsNumber(t *testing.T) {
	// The words are German. The number is what is read.
	r := &fakeRunner{
		out:  map[string]string{`net view \\10.0.0.9`: "Systemfehler 5 aufgetreten.\n\nZugriff verweigert.\n"},
		exit: map[string]int{`net view \\10.0.0.9`: 2},
	}
	_, err := netstat.ListShares(context.Background(), r, "10.0.0.9")
	var errno syscall.Errno
	if !errors.As(err, &errno) || errno != 5 {
		t.Errorf("err = %v, want Errno 5", err)
	}
}

func TestListSharesWithoutANumber(t *testing.T) {
	r := &fakeRunner{out: map[string]string{`net view \\h`: "boom"}, exit: map[string]int{`net view \\h`: 1}}
	_, err := netstat.ListShares(context.Background(), r, "h")
	if err == nil || strings.Contains(err.Error(), "Errno") {
		t.Errorf("err = %v", err)
	}
}

func TestListSharesReturnsTheShares(t *testing.T) {
	r := &fakeRunner{out: map[string]string{`net view \\192.168.1.5`: fixture(t, "netview_fr.txt")}}
	shares, err := netstat.ListShares(context.Background(), r, "192.168.1.5")
	if err != nil || len(shares) != 3 {
		t.Errorf("shares = %v, err = %v", shares, err)
	}
}

// fakeConn records sign-ins and sign-outs.
type fakeConn struct {
	mu           sync.Mutex
	connected    []string
	disconnected []string
	err          error
}

func (f *fakeConn) Connect(remote, user, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = append(f.connected, remote+"|"+user+"|"+password)
	return f.err
}

func (f *fakeConn) Disconnect(remote string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disconnected = append(f.disconnected, remote)
	return nil
}

func TestSignInPassesThePasswordInMemoryAndWrapsTheError(t *testing.T) {
	c := &fakeConn{}
	if err := netstat.SignIn(c, "h", "Games", netstat.Credentials{User: `  \bob `, Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	if c.connected[0] != `\\h\Games|bob|pw` {
		t.Errorf("connected = %v", c.connected)
	}
	c.err = syscall.Errno(1326)
	err := netstat.SignIn(c, "h", "Games", netstat.Credentials{User: "bob"})
	var errno syscall.Errno
	if !errors.As(err, &errno) || errno != 1326 {
		t.Errorf("err = %v", err)
	}
}

func TestCredentialsNeverPrintThePassword(t *testing.T) {
	c := netstat.Credentials{User: "bob", Password: "hunter2"}
	for _, s := range []string{c.String(), strings.TrimSpace(strings.Join([]string{c.GoString()}, ""))} {
		if strings.Contains(s, "hunter2") {
			t.Errorf("%q leaks the password", s)
		}
	}
}

func TestWithMicrosoftPrefix(t *testing.T) {
	if got := netstat.WithMicrosoftPrefix("me@example.com"); got != `MicrosoftAccount\me@example.com` {
		t.Error(got)
	}
	if got := netstat.WithMicrosoftPrefix(`PC\me`); got != `PC\me` {
		t.Error(got)
	}
}

func TestDropConnectionsClosesOnlyTheOnesToThatHost(t *testing.T) {
	r := &fakeRunner{out: map[string]string{"net use": fixture(t, "netuse_list.txt")}}
	c := &fakeConn{}
	n, err := netstat.DropConnections(context.Background(), r, c, "192.168.1.5")
	if err != nil || n != 2 {
		t.Fatalf("closed %d, err %v", n, err)
	}
	want := []string{`\\192.168.1.5\IPC$`, `\\192.168.1.5\Games`}
	if !reflect.DeepEqual(c.disconnected, want) {
		t.Errorf("disconnected = %q, want %q", c.disconnected, want)
	}
}

func TestBackoff(t *testing.T) {
	want := []time.Duration{2, 4, 8, 16, 30, 30, 30}
	for i, w := range want {
		if got := netstat.Backoff(i); got != w*time.Second {
			t.Errorf("Backoff(%d) = %v, want %v", i, got, w*time.Second)
		}
	}
}

// pipeDialer dials succeed after n failures.
func failingThenOK(failures int) (netstat.Dialer, *int) {
	calls := 0
	return func(context.Context, string, string) (net.Conn, error) {
		calls++
		if calls <= failures {
			return nil, errors.New("no route")
		}
		a, b := net.Pipe()
		_ = b.Close()
		return a, nil
	}, &calls
}

func TestWaitReachableBacksOffUntilTheHostAnswers(t *testing.T) {
	dial, calls := failingThenOK(3)
	var waits []time.Duration
	sleep := func(_ context.Context, d time.Duration) { waits = append(waits, d) }
	err := netstat.WaitReachable(context.Background(), dial, "h", sleep, nil)
	if err != nil || *calls != 4 {
		t.Fatalf("err = %v after %d dials", err, *calls)
	}
	if !reflect.DeepEqual(waits, []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}) {
		t.Errorf("waits = %v", waits)
	}
}

func TestWaitReachableStopsWhenCancelled(t *testing.T) {
	dial, _ := failingThenOK(1 << 30)
	ctx, cancel := context.WithCancel(context.Background())
	sleep := func(context.Context, time.Duration) { cancel() }
	if err := netstat.WaitReachable(ctx, dial, "h", sleep, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestReachableUsesPort445(t *testing.T) {
	var addr string
	dial := func(_ context.Context, _, a string) (net.Conn, error) {
		addr = a
		return nil, errors.New("x")
	}
	if netstat.Reachable(context.Background(), dial, "10.0.0.1", time.Second) || addr != "10.0.0.1:445" {
		t.Errorf("probed %q", addr)
	}
}

func TestMeterSmoothsAndSurvivesACounterReset(t *testing.T) {
	now := time.Unix(1000, 0)
	var in uint64
	m := netstat.NewMeter(func() (uint64, uint64, error) { return in, 0, nil }, func() time.Time { return now })

	if r, got := m.Sample(); r != 0 || got != 0 {
		t.Fatalf("first sample = %v, %d", r, got)
	}
	in += 100_000_000
	now = now.Add(time.Second)
	if r, got := m.Sample(); r != 100_000_000 || got != 100_000_000 {
		t.Errorf("second sample = %v, %d", r, got)
	}
	in += 200_000_000
	now = now.Add(time.Second)
	r, got := m.Sample()
	if r <= 100_000_000 || r >= 200_000_000 || got != 300_000_000 {
		t.Errorf("third sample = %v, %d; want a smoothed rate between the two", r, got)
	}
	// The adapter resets: the counter goes backwards. No negative speed.
	in = 5
	now = now.Add(time.Second)
	if r2, _ := m.Sample(); r2 != r {
		t.Errorf("a counter reset changed the rate to %v", r2)
	}
}

func TestMeterKeepsTheLastValueOnAReadError(t *testing.T) {
	now := time.Unix(0, 0)
	fail := false
	var in uint64
	m := netstat.NewMeter(func() (uint64, uint64, error) {
		if fail {
			return 0, 0, errors.New("gone")
		}
		return in, 0, nil
	}, func() time.Time { return now })
	m.Sample()
	in, now = 1000, now.Add(time.Second)
	r, _ := m.Sample()
	fail = true
	if r2, _ := m.Sample(); r2 != r {
		t.Errorf("rate after a failed read = %v, want %v", r2, r)
	}
}

func TestETA(t *testing.T) {
	if got := netstat.ETA(100, 10); got != 10*time.Second {
		t.Error(got)
	}
	if netstat.ETA(100, 0) != 0 || netstat.ETA(0, 10) != 0 || netstat.ETA(-5, 10) != 0 {
		t.Error("an unknown speed or nothing left should give no ETA")
	}
}
