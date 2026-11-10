package share

import (
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
)

// TestAnEmptyListAsksForTheSignInFirst: a PC that lists nothing before a
// sign-in gets the sign-in form, not "no shared folders".
func TestAnEmptyListAsksForTheSignInFirst(t *testing.T) {
	r := newRecvFake()
	r.shares = nil
	r.signHost = []netstat.Share{{Name: "Games", Type: "Disk"}}
	h := recvIn(t, r).typed("192.168.1.5").send(enter())
	if s := asRecv(h); s.stage != recvCreds || s.credsFor != credsForList {
		t.Fatalf("stage = %v (for %v), want the sign-in for the list", s.stage, s.credsFor)
	}
	h.typed("devpit-ab12").send(enter()).typed("123456").send(enter())
	if s := asRecv(h); s.stage != recvShares {
		t.Fatalf("stage = %v, want the shared folders after signing in", s.stage)
	}
}

// TestAnEmptyListAfterSigningInSaysSo: signed in and still nothing is the
// plain "no shared folders" error.
func TestAnEmptyListAfterSigningInSaysSo(t *testing.T) {
	r := newRecvFake()
	r.shares, r.signHost = nil, nil
	h := recvIn(t, r).typed("192.168.1.5").send(enter()).typed("bob").send(enter()).typed("x").send(enter())
	if s := asRecv(h); s.stage != recvError {
		t.Fatalf("stage = %v, want the error", s.stage)
	}
}
