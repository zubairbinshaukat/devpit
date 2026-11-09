package share

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
)

// freshRecv opens the receiving screen the way the menu does: build it, run
// Init, and hand it keys. Nothing else has happened to it yet.
func freshRecv(t *testing.T, r *fakeReceiver) *harness {
	t.Helper()
	scr := newRecvScreen(testDeps(newFakeHoster(), r, new([]string)), config.Default(), nil)
	return newHarness(t, scr).init()
}

func ipValue(h *harness) string { return h.scr.(recvScreen).ip.Value() }

func TestTheAddressFieldTakesTypingTheFirstTimeTheScreenOpens(t *testing.T) {
	h := freshRecv(t, newRecvFake())
	if !h.scr.(recvScreen).ip.Focused() {
		t.Fatal("the address field is not focused when the screen opens")
	}
	h.typed("192.168.1.5")
	if got := ipValue(h); got != "192.168.1.5" {
		t.Fatalf("typed 192.168.1.5 on the first try, the field holds %q", got)
	}
	if v := h.view(); !strings.Contains(v, "192.168.1.5") {
		t.Errorf("what was typed is not on screen:\n%s", v)
	}
}

func TestTheFirstAddressIsWhatIsLookedUp(t *testing.T) {
	r := newRecvFake()
	h := freshRecv(t, r)
	h.typed("10.0.0.7").send(keyCode(tea.KeyEnter))
	if got := r.called("list 10.0.0.7"); got != 1 {
		t.Fatalf("the typed address was not the one looked up (calls: %v)", r.calls)
	}
}

func TestTheAddressFieldTakesPaste(t *testing.T) {
	h := freshRecv(t, newRecvFake())
	h.send(tea.PasteMsg{Content: "192.168.1.9"})
	if got := ipValue(h); got != "192.168.1.9" {
		t.Fatalf("pasted address: field holds %q", got)
	}
}

func TestTabAndShiftTabInTheSignInFormSwitchFieldsAndKeepTyping(t *testing.T) {
	h := freshRecv(t, newRecvFake())
	toShares(h)
	h.send(keyCode(tea.KeyEnter)) // pick Games -> sign-in form
	s := h.scr.(recvScreen)
	if s.stage != recvCreds || !s.user.Focused() || s.pass.Focused() {
		t.Fatalf("sign-in form: stage %d, user focused %v, pass focused %v", s.stage, s.user.Focused(), s.pass.Focused())
	}
	h.typed("bob")
	h.send(keyCode(tea.KeyTab))
	if s = h.scr.(recvScreen); !s.pass.Focused() || s.user.Focused() {
		t.Fatalf("Tab did not move to the password field")
	}
	h.typed("pw")
	h.send(tea.PasteMsg{Content: "x"})
	h.send(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if s = h.scr.(recvScreen); !s.user.Focused() || s.pass.Focused() {
		t.Fatalf("Shift+Tab did not move back to the user field")
	}
	s = h.scr.(recvScreen)
	if s.user.Value() != "bob" || s.pass.Value() != "pwx" {
		t.Fatalf("user %q, pass %q; want bob, pwx", s.user.Value(), s.pass.Value())
	}
}

func TestEnterOnAValidAddressMovesOnAndEscGoesBack(t *testing.T) {
	r := newRecvFake()
	h := freshRecv(t, r)
	h.typed("192.168.1.5").send(keyCode(tea.KeyEnter))
	if s := h.scr.(recvScreen); s.stage != recvShares {
		t.Fatalf("stage %d after Enter on a valid address, want the share list", s.stage)
	}
	// Esc is left to the shell, which pops the screen; the screen itself must
	// not swallow it into a field.
	next, _ := h.scr.Update(keyCode(tea.KeyEscape), h.ctx)
	if s := next.(recvScreen); s.ip.Value() != "192.168.1.5" || s.stage != recvShares {
		t.Errorf("Esc changed the screen: address %q, stage %d", s.ip.Value(), s.stage)
	}
}

func TestTryingAgainAfterAnErrorStillTakesTyping(t *testing.T) {
	r := newRecvFake()
	r.listErr = errors.New("no such PC")
	h := freshRecv(t, r)
	h.send(keyCode(tea.KeyEnter)) // empty address -> error
	if h.scr.(recvScreen).stage != recvError {
		t.Fatalf("stage %d, want the error screen", h.scr.(recvScreen).stage)
	}
	h.send(keyCode(tea.KeyEnter)) // back to the address
	h.typed("1.2.3.4")
	if got := ipValue(h); got != "1.2.3.4" {
		t.Fatalf("after an error the field holds %q", got)
	}
}
