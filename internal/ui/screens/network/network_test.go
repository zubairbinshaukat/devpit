package network

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/network"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// testCtx is the render context every screen test in this package uses: the
// dark theme, the unicode icon tier, default config, at the plan's 100×30
// reference size.
func testCtx() uictx.Context {
	return uictx.Context{
		Theme:      theme.For(true),
		Icons:      icons.Unicode(),
		Config:     config.Default(),
		Width:      100,
		Height:     30,
		BodyHeight: 26,
	}
}

func enterKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 13} }

// TestPingStreamsLinesThenSummary drives the ping screen's input → running →
// done flow with an injected runFn, asserting that streamed lines land in
// the view before the final Sent/Received/Min/Avg/Max summary appears.
func TestPingStreamsLinesThenSummary(t *testing.T) {
	var calledHost string
	m := newPingScreen()
	m.runFn = func(_ context.Context, host string, _ int, onLine func(string), _ network.Options) (network.PingResult, error) {
		calledHost = host
		onLine("Pinging 1.1.1.1 with 32 bytes of data:")
		onLine("Reply from 1.1.1.1: bytes=32 time=10ms TTL=55")
		return network.PingResult{Sent: 4, Received: 4, MinMs: 9, AvgMs: 10, MaxMs: 12}, nil
	}

	ctx := testCtx()
	var scr uictx.Screen = m

	next, cmd := scr.Update(enterKey(), ctx)
	scr = next
	if cmd == nil {
		t.Fatal("Enter on the host field produced no command")
	}

	bm, ok := cmd().(tea.BatchMsg)
	if !ok || len(bm) != 2 {
		t.Fatalf("expected a 2-command batch, got %T", cmd())
	}

	// bm[0] is startPingCmd: run it first (synchronously, off the real ping
	// binary) so the fake runFn has already streamed its lines into the
	// buffer, but hold its pingDoneMsg back for a moment.
	doneMsg := bm[0]()
	if _, ok := doneMsg.(pingDoneMsg); !ok {
		t.Fatalf("bm[0] produced %T, want pingDoneMsg", doneMsg)
	}

	// bm[1] is the 150 ms drain tick: applying it now, before pingDoneMsg,
	// proves the lines really did stream into the running view rather than
	// only appearing once the run had already finished.
	tickMsg := bm[1]()
	if _, ok := tickMsg.(pingTickMsg); !ok {
		t.Fatalf("bm[1] produced %T, want pingTickMsg", tickMsg)
	}
	scr, _ = scr.Update(tickMsg, ctx)

	if calledHost != defaultPingHost {
		t.Fatalf("runFn called with host %q, want default %q", calledHost, defaultPingHost)
	}

	running := scr.View(ctx)
	if !strings.Contains(running, "Reply from 1.1.1.1") {
		t.Fatalf("expected a streamed line in the running view:\n%s", running)
	}

	scr, _ = scr.Update(doneMsg, ctx)
	done := scr.View(ctx)
	if !strings.Contains(done, "Sent 4, received 4") {
		t.Fatalf("expected the summary line in the done view:\n%s", done)
	}
}

// TestPublicIPOfflineDoesNotBlock asserts that an offline public-IP lookup
// (network.PublicIP's *OfflineError) surfaces as a plain "offline" message
// and that the lookup command itself returns promptly rather than hanging
// Update.
func TestPublicIPOfflineDoesNotBlock(t *testing.T) {
	m := newIPScreen()
	m.localsFn = func() ([]network.Iface, error) { return nil, nil }
	m.publicFn = func(context.Context) (net.IP, error) {
		return nil, &network.OfflineError{Err: errors.New("no route to host")}
	}

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned no command")
	}

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	select {
	case msg := <-done:
		ctx := testCtx()
		var scr uictx.Screen = m
		if bm, ok := msg.(tea.BatchMsg); ok {
			for _, c := range bm {
				scr, _ = scr.Update(c(), ctx)
			}
		} else {
			scr, _ = scr.Update(msg, ctx)
		}
		view := scr.View(ctx)
		if !strings.Contains(view, "Offline") {
			t.Fatalf("expected an offline message in the view:\n%s", view)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Init()'s command did not return: an offline lookup must never block")
	}
}

// TestFlushDNSDefaultsToNo is safety rule 2 pinned at the screen level: the
// flush-DNS screen opens on a confirm dialog, and pressing Enter without an
// explicit yes must never invoke FlushDNS.
func TestFlushDNSDefaultsToNo(t *testing.T) {
	calls := 0
	m := newDNSScreen()
	m.flushFn = func(context.Context) (string, error) {
		calls++
		return "ok", nil
	}

	ctx := testCtx()
	var scr uictx.Screen = m

	next, cmd := scr.Update(enterKey(), ctx)
	scr = next
	if cmd == nil {
		t.Fatal("Enter on a fresh confirm dialog produced no command")
	}
	msg := cmd()
	ans, ok := msg.(confirm.AnsweredMsg)
	if !ok {
		t.Fatalf("expected confirm.AnsweredMsg, got %T", msg)
	}
	if ans.Answer != confirm.AnswerNo {
		t.Fatalf("default answer = %v, want No", ans.Answer)
	}

	_, _ = scr.Update(ans, ctx)
	if calls != 0 {
		t.Fatalf("FlushDNS invoked %d times before an explicit yes, want 0", calls)
	}
}
