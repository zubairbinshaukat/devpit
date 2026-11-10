package share

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/share/host"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

func (f *fakeHoster) Suggest(context.Context) host.Suggestion { return f.sugg }

// hotspotHoster is a sharing PC on Wi-Fi 6 whose hotspot is suggested.
func hotspotHoster() *fakeHoster {
	f := newFakeHoster()
	wifi := host.Adapter{
		Name: "Wi-Fi", Index: 12, IP: net.ParseIP("192.168.1.20"), Best: true,
		Wireless: true, Standard: "Wi-Fi 6", LinkBps: 1_201_000_000,
	}
	f.adapters = []host.Adapter{wifi}
	f.sugg = host.Suggestion{
		Offer: true, Suggested: true, Reason: "suggested", Adapter: wifi,
		SavedSSID: "OMEN-Hotspot", SavedBand: "5", SavedPassword: "sunny-day-42",
		Band5Supported: true, TempSupported: true,
	}
	netUse, robo, cleanup := host.Commands("192.168.137.1", "Games-x7k2", "devpit-x7k2", "482913")
	f.hsCard = host.Card{
		IP: "192.168.137.1", Adapter: "Direct hotspot", Share: "Games-x7k2", User: "devpit-x7k2", Password: "482913",
		Path: `D:\Games`, NetUse: netUse, Robocopy: robo, Cleanup: cleanup,
		Mode: host.ModeHotspot, SSID: "OMEN-Hotspot", HotspotPassword: "sunny-day-42", OwnPassword: true, Band: "5",
		NormalIP: "192.168.1.20",
	}
	return f
}

func hostHotspotStates() []goldenState {
	base := func(t *testing.T, f *fakeHoster) *harness { return newHostHarness(t, f, new([]string)) }
	return []goldenState{
		{"host_hotspot_suggested", func(t *testing.T) uictx.Screen {
			return base(t, hotspotHoster()).send(chosen(`D:\Games`)).scr
		}},
		{"host_hotspot_offered_lower", func(t *testing.T) uictx.Screen {
			f := hotspotHoster()
			f.sugg.Suggested, f.sugg.Hint = false, "May be slower than your router."
			return base(t, f).send(chosen(`D:\Games`)).scr
		}},
		{"host_hotspot_band_options", func(t *testing.T) uictx.Screen {
			f := hotspotHoster()
			f.sugg.SavedBand, f.sugg.NeedsBandChoice = "2.4", true
			return base(t, f).send(chosen(`D:\Games`), down(), enter()).scr
		}},
		{"host_hotspot_band_options_no_temp", func(t *testing.T) uictx.Screen {
			f := hotspotHoster()
			f.sugg.SavedBand, f.sugg.NeedsBandChoice, f.sugg.TempSupported = "auto", true, false
			return base(t, f).send(chosen(`D:\Games`), down(), enter()).scr
		}},
		{"host_hotspot_setup", func(t *testing.T) uictx.Screen {
			s := base(t, hotspotHoster()).scr.(hostScreen)
			s.stage, s.opts = hostSetup, host.Options{Mode: host.ModeHotspot, Hotspot: host.HotspotSavedSwitchTo5}
			s.steps, s.stepAt = []host.Step{{Key: "band5"}, {Key: "hotspot"}}, 1
			return s
		}},
		{"host_hotspot_card", func(t *testing.T) uictx.Screen {
			return base(t, hotspotHoster()).send(chosen(`D:\Games`), down(), enter()).scr
		}},
		{"host_hotspot_card_temp", func(t *testing.T) uictx.Screen {
			f := hotspotHoster()
			f.hsCard.SSID, f.hsCard.HotspotPassword, f.hsCard.OwnPassword, f.hsCard.NormalIP = "Devpit-x7k2", "50318274", false, ""
			return base(t, f).send(chosen(`D:\Games`), down(), enter()).scr
		}},
		{"host_hotspot_fallback_private", func(t *testing.T) uictx.Screen {
			f := hotspotHoster()
			f.hsErr = &host.HotspotFallbackError{
				Key: "wifi-off", Message: "Wi-Fi is turned off, so a hotspot cannot start.", Err: host.ErrNeedPrivate,
			}
			return base(t, f).send(chosen(`D:\Games`), down(), enter()).scr
		}},
		{"host_card_link_speed", func(t *testing.T) uictx.Screen {
			f := newFakeHoster()
			f.card.Standard, f.card.LinkBps, f.card.Wireless = "Wi-Fi 6", 1_201_000_000, true
			return base(t, f).send(chosen(`D:\Games`)).scr
		}},
	}
}

// TestTheSuggestedRowSaysSoInWords pins rule 62's wording: "suggested" is a
// word on the row, not only a colour.
func TestTheSuggestedRowSaysSoInWords(t *testing.T) {
	h := newHostHarness(t, hotspotHoster(), new([]string)).send(chosen(`D:\Games`))
	if !strings.Contains(h.view(), "Direct hotspot (suggested)") {
		t.Fatalf("no suggested row:\n%s", h.view())
	}
}

// TestHotspotModeIsOnlyStartedWhenChosen: an offered row that is not picked
// starts a normal share.
func TestHotspotModeIsOnlyStartedWhenChosen(t *testing.T) {
	f := hotspotHoster()
	newHostHarness(t, f, new([]string)).send(chosen(`D:\Games`), enter())
	if len(f.started) != 1 || f.started[0].Mode != host.ModeNormal {
		t.Fatalf("started = %+v, want one normal share", f.started)
	}
}

// TestHotspotModeNeverAsksToSwitchTheNetworkFirst: picking the hotspot row
// starts at once, with the saved settings, and no Private question.
func TestHotspotModeNeverAsksToSwitchTheNetworkFirst(t *testing.T) {
	f := hotspotHoster()
	f.cat = host.CategoryPublic
	h := newHostHarness(t, f, new([]string)).send(chosen(`D:\Games`), down(), enter())
	if s := h.scr.(hostScreen); s.stage != hostCard {
		t.Fatalf("stage = %v, want the card", s.stage)
	}
	if len(f.started) != 1 || f.started[0].Mode != host.ModeHotspot || f.started[0].Hotspot != host.HotspotSaved {
		t.Fatalf("started = %+v, want one hotspot share with the saved settings", f.started)
	}
}

// TestTheBandChoiceIsPassedOn: each band option starts with its own choice.
func TestTheBandChoiceIsPassedOn(t *testing.T) {
	for _, tt := range []struct {
		downs int
		want  host.HotspotChoice
	}{{0, host.HotspotSavedSwitchTo5}, {1, host.HotspotTemp}, {2, host.HotspotSaved}} {
		f := hotspotHoster()
		f.sugg.SavedBand, f.sugg.NeedsBandChoice = "2.4", true
		h := newHostHarness(t, f, new([]string)).send(chosen(`D:\Games`), down(), enter())
		for range tt.downs {
			h.send(down())
		}
		h.send(enter())
		if len(f.started) != 1 || f.started[0].Hotspot != tt.want {
			t.Errorf("after %d downs started = %+v, want %v", tt.downs, f.started, tt.want)
		}
	}
}

// TestTheBandScreenSaysTheChangeStays pins rule 69: the switch is chosen on a
// screen that says it stays.
func TestTheBandScreenSaysTheChangeStays(t *testing.T) {
	f := hotspotHoster()
	f.sugg.SavedBand, f.sugg.NeedsBandChoice = "2.4", true
	h := newHostHarness(t, f, new([]string)).send(chosen(`D:\Games`), down(), enter())
	if !strings.Contains(h.view(), "stays on 5 GHz") {
		t.Fatalf("the band screen does not say the change stays:\n%s", h.view())
	}
}

// TestAHotspotFallbackOnAPublicNetworkAsksThenSharesNormally: yes shares over
// the normal network, switched to Private, with the same engine.
func TestAHotspotFallbackOnAPublicNetworkAsksThenSharesNormally(t *testing.T) {
	f := hotspotHoster()
	f.hsErr = &host.HotspotFallbackError{Key: "wifi-off", Message: "Wi-Fi is turned off.", Err: host.ErrNeedPrivate}
	h := newHostHarness(t, f, new([]string)).send(chosen(`D:\Games`), down(), enter())
	if s := h.scr.(hostScreen); s.stage != hostPrivate {
		t.Fatalf("stage = %v, want the Private question", s.stage)
	}
	h.send(text("y"))
	if len(f.started) != 2 || f.started[1].Mode != host.ModeNormal || !f.started[1].MakePrivate {
		t.Fatalf("started = %+v, want a normal share switched to Private", f.started)
	}
}

// TestTheHotspotCardCopiesTheHotspotPassword: w copies it.
func TestTheHotspotCardCopiesTheHotspotPassword(t *testing.T) {
	var copied []string
	newHostHarness(t, hotspotHoster(), &copied).send(chosen(`D:\Games`), down(), enter(), text("w"))
	if len(copied) != 1 || copied[0] != "sunny-day-42" {
		t.Fatalf("copied = %q, want the hotspot password", copied)
	}
}

// TestNoHostHotspotFramePromisesASpeed (rule 68).
func TestNoHostHotspotFramePromisesASpeed(t *testing.T) {
	for _, st := range hostHotspotStates() {
		frame := strings.ToLower(frameWithKeys(st.build(t), testCtx(80, 24, icons.TierUnicode)))
		for _, bad := range []string{"faster", "up to", "×"} {
			if strings.Contains(frame, bad) {
				t.Errorf("%s says %q:\n%s", st.name, bad, frame)
			}
		}
	}
}

// TestSharingOverTheNetworkStaysFirstAndSelected: in the first real test the
// hotspot was slower than the router, so Enter on the list shares over the
// network and the hotspot is a suggestion lower down.
func TestSharingOverTheNetworkStaysFirstAndSelected(t *testing.T) {
	h := newHostHarness(t, hotspotHoster(), new([]string)).send(chosen(`D:\Games`))
	s := h.scr.(hostScreen)
	if it, ok := s.adMenu.Selected(); !ok || it.ID == hotspotRowID {
		t.Fatalf("selected = %+v, want the network row first", it)
	}
}
