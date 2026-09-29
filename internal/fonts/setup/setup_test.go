package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/fonts"
	"github.com/zubairbinshaukat/devpit/internal/wt"
)

const fontPath = `C:\fonts\SymbolsNerdFontMono-Regular.ttf`

// fakeDeps returns a Deps whose every call is a fake that records what it
// was asked, so a test can assert on the calls without any of them
// reaching the real registry, network or settings files. Tests override the
// fields they care about.
func fakeDeps(locs []wt.Location) (Deps, *calls) {
	c := &calls{}
	return Deps{
		Status: func(fonts.Options) (fonts.State, error) {
			return fonts.State{Installed: true, FontPath: fontPath}, nil
		},
		Install: func(context.Context, fonts.Options) (fonts.Result, error) {
			return fonts.Result{Installed: true, FontPath: fontPath}, nil
		},
		Remove:        func(fonts.Options) error { c.removed = true; return nil },
		FindTerminals: func(wt.FindOptions) []wt.Location { return locs },
		Patch: func(path, fallback string) (wt.PatchResult, error) {
			c.patched = append(c.patched, path)
			c.fallback = fallback
			return wt.PatchResult{Changed: true, BackupPath: path + ".devpit-bak", Fallback: fallback}, nil
		},
		Restore: func(path string) error { c.restored = append(c.restored, path); return nil },
	}, c
}

// calls records what the fakes in fakeDeps were asked to do.
type calls struct {
	removed  bool
	patched  []string
	restored []string
	fallback string
}

var twoTerminals = []wt.Location{
	{Path: `C:\wt\stable\settings.json`, Flavor: wt.FlavorStable},
	{Path: `C:\wt\preview\settings.json`, Flavor: wt.FlavorPreview},
}

// TestInstallFreshPatchesEveryTerminal checks a fresh install patches every
// Terminal found with FallbackFont and reports each file.
func TestInstallFreshPatchesEveryTerminal(t *testing.T) {
	deps, c := fakeDeps(twoTerminals)

	out, err := Install(context.Background(), deps)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if out.Kind != Installed || out.FontPath != fontPath {
		t.Fatalf("outcome = %+v, want Installed at %s", out, fontPath)
	}
	if !reflect.DeepEqual(c.patched, []string{twoTerminals[0].Path, twoTerminals[1].Path}) {
		t.Errorf("patched %v, want every location", c.patched)
	}
	if c.fallback != FallbackFont {
		t.Errorf("fallback = %q, want %q", c.fallback, FallbackFont)
	}
	if len(out.Terminals) != 2 || !out.Terminals[0].Changed || out.Terminals[1].Location != twoTerminals[1] {
		t.Errorf("terminals = %+v", out.Terminals)
	}
	if !out.OK() {
		t.Error("a fresh install must be OK")
	}
}

// TestInstallAlreadyInstalledStillPatches checks an existing font skips
// straight to patching: a Terminal installed after the font must still get
// the fallback.
func TestInstallAlreadyInstalledStillPatches(t *testing.T) {
	deps, c := fakeDeps(twoTerminals[:1])
	deps.Install = func(context.Context, fonts.Options) (fonts.Result, error) {
		return fonts.Result{Installed: false, FontPath: fontPath}, nil
	}
	deps.Patch = func(path, _ string) (wt.PatchResult, error) {
		c.patched = append(c.patched, path)
		return wt.PatchResult{}, nil // already patched: idempotent no-op
	}

	out, err := Install(context.Background(), deps)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if out.Kind != AlreadyInstalled {
		t.Fatalf("Kind = %v, want AlreadyInstalled", out.Kind)
	}
	if len(c.patched) != 1 || out.Terminals[0].Changed {
		t.Errorf("patched %v, terminals %+v: want one unchanged patch", c.patched, out.Terminals)
	}
}

// TestInstallUnparsableTerminalIsNotAFailure checks one unpatchable
// settings.json is recorded against its Terminal while the install itself
// still succeeds and the other Terminal is patched.
func TestInstallUnparsableTerminalIsNotAFailure(t *testing.T) {
	deps, _ := fakeDeps(twoTerminals)
	bad := &wt.UnparsableError{Path: twoTerminals[1].Path, Err: errors.New("bad json")}
	deps.Patch = func(path, _ string) (wt.PatchResult, error) {
		if path == twoTerminals[1].Path {
			return wt.PatchResult{}, bad
		}
		return wt.PatchResult{Changed: true}, nil
	}

	out, err := Install(context.Background(), deps)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !out.OK() {
		t.Fatal("an unparsable settings.json must not fail the install")
	}
	if !out.Terminals[0].Changed || out.Terminals[0].Err != nil {
		t.Errorf("first terminal = %+v, want patched", out.Terminals[0])
	}
	if out.Terminals[1].Changed || !errors.Is(out.Terminals[1].Err, bad) {
		t.Errorf("second terminal = %+v, want its UnparsableError", out.Terminals[1])
	}
}

// TestInstallOfflineAndPolicyAreOutcomesNotErrors checks the two expected
// failure modes come back as Kinds with a nil error, never patch a
// Terminal, and leave the config alone.
func TestInstallOfflineAndPolicyAreOutcomesNotErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		want  Kind
		steps string
	}{
		{"offline", fmt.Errorf("wrapped: %w", &fonts.OfflineError{Err: errors.New("dial tcp: no route")}), Offline, ""},
		{"policy", &fonts.PolicyError{Err: errors.New("access denied"), ManualSteps: "Ask an admin."}, PolicyBlocked, "Ask an admin."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, c := fakeDeps(twoTerminals)
			deps.Install = func(context.Context, fonts.Options) (fonts.Result, error) { return fonts.Result{}, tc.err }

			out, err := Install(context.Background(), deps)
			if err != nil {
				t.Fatalf("Install returned %v, want a nil error", err)
			}
			if out.Kind != tc.want || out.ManualSteps != tc.steps {
				t.Fatalf("outcome = %+v, want Kind %v steps %q", out, tc.want, tc.steps)
			}
			if len(c.patched) != 0 {
				t.Errorf("patched %v with no font installed", c.patched)
			}
			if out.OK() {
				t.Error("OK() must be false")
			}
			cfg := config.Default()
			if got := out.Apply(cfg); !reflect.DeepEqual(got, cfg) {
				t.Errorf("Apply changed the config: %+v", got)
			}
		})
	}
}

// TestInstallUnexpectedErrorIsReturned checks any other failure is returned
// as-is with a zero Outcome, so it can never read as a success.
func TestInstallUnexpectedErrorIsReturned(t *testing.T) {
	want := errors.New("fonts: checksum mismatch")
	deps, _ := fakeDeps(nil)
	deps.Install = func(context.Context, fonts.Options) (fonts.Result, error) { return fonts.Result{}, want }

	out, err := Install(context.Background(), deps)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if out.OK() || out.Kind != 0 {
		t.Errorf("outcome = %+v, want the zero Outcome", out)
	}
}

// TestRemoveRestoresThenRemoves checks Remove restores every Terminal,
// treats a missing backup as nothing to do, records any other restore
// failure, and still removes the font.
func TestRemoveRestoresThenRemoves(t *testing.T) {
	locs := append(append([]wt.Location{}, twoTerminals...), wt.Location{Path: `C:\wt\portable\settings.json`, Flavor: wt.FlavorUnpackaged})
	deps, c := fakeDeps(locs)
	denied := errors.New("access denied")
	deps.Restore = func(path string) error {
		c.restored = append(c.restored, path)
		switch path {
		case locs[1].Path:
			return fmt.Errorf("wt: read backup: %w", fs.ErrNotExist)
		case locs[2].Path:
			return denied
		}
		return nil
	}

	out, err := Remove(deps)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if out.Kind != Removed || !c.removed {
		t.Fatalf("outcome = %+v, removed = %v", out, c.removed)
	}
	if len(c.restored) != 3 {
		t.Fatalf("restored %v, want every location", c.restored)
	}
	if !out.Terminals[0].Changed || out.Terminals[1].Changed || out.Terminals[1].Err != nil {
		t.Errorf("terminals = %+v: want the first restored and the backup-less one silently skipped", out.Terminals)
	}
	if !errors.Is(out.Terminals[2].Err, denied) {
		t.Errorf("third terminal err = %v, want %v", out.Terminals[2].Err, denied)
	}

	cfg := config.Default()
	cfg.FontInstalled, cfg.GlyphsConfirmed, cfg.Icons = true, true, config.IconsNerd
	got := out.Apply(cfg)
	if got.FontInstalled || got.GlyphsConfirmed || got.Icons != config.IconsAuto {
		t.Errorf("Apply = FontInstalled %v GlyphsConfirmed %v Icons %q, want false false auto",
			got.FontInstalled, got.GlyphsConfirmed, got.Icons)
	}
}

// TestRemoveNotInstalledTouchesNothing checks Remove leaves Terminals and
// the font alone when there is no font, and only clears the config flag.
func TestRemoveNotInstalledTouchesNothing(t *testing.T) {
	deps, c := fakeDeps(twoTerminals)
	deps.Status = func(fonts.Options) (fonts.State, error) { return fonts.State{}, nil }

	out, err := Remove(deps)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if out.Kind != NotInstalled || c.removed || len(c.restored) != 0 {
		t.Fatalf("outcome = %+v, removed %v, restored %v: want nothing touched", out, c.removed, c.restored)
	}
	cfg := config.Default()
	cfg.FontInstalled, cfg.GlyphsConfirmed, cfg.Icons = true, true, config.IconsNerd
	got := out.Apply(cfg)
	if got.FontInstalled || !got.GlyphsConfirmed || got.Icons != config.IconsNerd {
		t.Errorf("Apply = %+v: want only FontInstalled cleared", got)
	}
}

// TestRemoveFontErrorIsReturned checks a failed fonts.Remove fails the run
// after the Terminals were already restored.
func TestRemoveFontErrorIsReturned(t *testing.T) {
	want := errors.New("fonts: delete registry value: denied")
	deps, c := fakeDeps(twoTerminals)
	deps.Remove = func(fonts.Options) error { return want }

	if _, err := Remove(deps); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if len(c.restored) != 2 {
		t.Errorf("restored %v, want the Terminals restored before the font removal", c.restored)
	}
}

// TestApplyInstalledLeavesTierAlone checks an install flips only
// FontInstalled: the glyph probe, not the install, turns nerd icons on.
func TestApplyInstalledLeavesTierAlone(t *testing.T) {
	cfg := config.Default()
	for _, k := range []Kind{Installed, AlreadyInstalled} {
		got := Outcome{Kind: k}.Apply(cfg)
		if !got.FontInstalled || got.GlyphsConfirmed || got.Icons != cfg.Icons {
			t.Errorf("Apply(%v) = %+v", k, got)
		}
	}
}

// TestStatusUsesDeps checks Status goes through the injected call.
func TestStatusUsesDeps(t *testing.T) {
	deps, _ := fakeDeps(nil)
	st, err := Status(deps)
	if err != nil || !st.Installed || st.FontPath != fontPath {
		t.Fatalf("Status = %+v, %v", st, err)
	}
}
