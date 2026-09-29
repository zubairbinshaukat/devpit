package devpit

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/fonts"
	"github.com/zubairbinshaukat/devpit/internal/fonts/setup"
	"github.com/zubairbinshaukat/devpit/internal/wt"
)

const (
	testFontPath = `C:\fonts\SymbolsNerdFontMono-Regular.ttf`
	testWTPath   = `C:\wt\stable\settings.json`
)

// fakeFontEnv is a Windows fontEnv whose engine calls all succeed against
// one fake Terminal, with a config that loads as fresh defaults and a save
// that records what it was handed. Tests override the pieces they exercise.
func fakeFontEnv() (*fontEnv, *[]config.Config) {
	var saved []config.Config
	env := &fontEnv{
		windows: true,
		deps: setup.Deps{
			Status: func(fonts.Options) (fonts.State, error) {
				return fonts.State{Installed: true, FontPath: testFontPath}, nil
			},
			Install: func(context.Context, fonts.Options) (fonts.Result, error) {
				return fonts.Result{Installed: true, FontPath: testFontPath}, nil
			},
			Remove: func(fonts.Options) error { return nil },
			FindTerminals: func(wt.FindOptions) []wt.Location {
				return []wt.Location{{Path: testWTPath, Flavor: wt.FlavorStable}}
			},
			Patch: func(path, _ string) (wt.PatchResult, error) {
				return wt.PatchResult{Changed: true, BackupPath: path + ".devpit-bak"}, nil
			},
			Restore: func(string) error { return nil },
		},
		loadConfig: func() (config.Result, error) {
			return config.Result{Config: config.Default(), Created: true}, nil
		},
		saveConfig: func(c config.Config) error { saved = append(saved, c); return nil },
	}
	return env, &saved
}

// runFont runs `devpit font <args>` against env and returns stdout, stderr
// and the command's error (non-nil is exit code 1 under Execute).
func runFont(t *testing.T, env *fontEnv, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newFontCmdWith(*env)
	// NewRootCmd silences these for the whole tree; mirror it so the
	// output checked here is what a user sees.
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

// wantLines fails unless got is exactly want, one entry per line.
func wantLines(t *testing.T, got string, want ...string) {
	t.Helper()
	if g, w := got, strings.Join(want, "\n")+"\n"; g != w {
		t.Errorf("output:\n%s\nwant:\n%s", g, w)
	}
}

// TestFontInstallFresh checks a fresh install prints the result plus one
// line per patched Terminal and saves FontInstalled.
func TestFontInstallFresh(t *testing.T) {
	env, saved := fakeFontEnv()
	out, _, err := runFont(t, env, "install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	wantLines(t, out,
		"✔ Icon font installed (Symbols Nerd Font Mono)",
		"  ✔ Windows Terminal: added as fallback font ("+testWTPath+")")
	if len(*saved) != 1 || !(*saved)[0].FontInstalled {
		t.Fatalf("saved %+v, want one save with FontInstalled", *saved)
	}
	if (*saved)[0].Icons != config.Default().Icons || (*saved)[0].GlyphsConfirmed {
		t.Error("install must leave the icon tier to the glyph probe")
	}
}

// TestFontInstallAlreadyNoTerminal checks the already-installed wording
// and the hint shown when no Windows Terminal is found.
func TestFontInstallAlreadyNoTerminal(t *testing.T) {
	env, saved := fakeFontEnv()
	env.deps.Install = func(context.Context, fonts.Options) (fonts.Result, error) {
		return fonts.Result{Installed: false, FontPath: testFontPath}, nil
	}
	env.deps.FindTerminals = func(wt.FindOptions) []wt.Location { return nil }

	out, _, err := runFont(t, env, "install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	wantLines(t, out,
		"✔ Icon font already installed",
		`  • Windows Terminal not found; set "Symbols Nerd Font Mono" as a fallback font in your terminal`)
	if len(*saved) != 1 || !(*saved)[0].FontInstalled {
		t.Fatalf("saved %+v, want FontInstalled saved", *saved)
	}
}

// TestFontInstallTerminalLines checks the unchanged and unpatchable
// Terminal wordings, and that --quiet drops every sub-line.
func TestFontInstallTerminalLines(t *testing.T) {
	env, _ := fakeFontEnv()
	env.deps.FindTerminals = func(wt.FindOptions) []wt.Location {
		return []wt.Location{{Path: `C:\a\settings.json`}, {Path: `C:\b\settings.json`}}
	}
	env.deps.Patch = func(path, _ string) (wt.PatchResult, error) {
		if path == `C:\b\settings.json` {
			return wt.PatchResult{}, &wt.UnparsableError{Path: path, Err: errors.New("bad json")}
		}
		return wt.PatchResult{}, nil
	}

	out, _, err := runFont(t, env, "install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	wantLines(t, out,
		"✔ Icon font installed (Symbols Nerd Font Mono)",
		`  ✔ Windows Terminal: fallback font already set (C:\a\settings.json)`,
		`  ! Windows Terminal: not changed, add "Symbols Nerd Font Mono" to profiles.defaults.font.face yourself (wt: C:\b\settings.json is not valid HuJSON: bad json)`)

	out, _, err = runFont(t, env, "install", "--quiet")
	if err != nil {
		t.Fatalf("install --quiet: %v", err)
	}
	wantLines(t, out, "✔ Icon font installed (Symbols Nerd Font Mono)")
}

// TestFontInstallOffline checks no internet is a note with exit code 0
// and saves nothing.
func TestFontInstallOffline(t *testing.T) {
	env, saved := fakeFontEnv()
	env.deps.Install = func(context.Context, fonts.Options) (fonts.Result, error) {
		return fonts.Result{}, &fonts.OfflineError{Err: errors.New("dial tcp: no route to host")}
	}

	for _, args := range [][]string{{"install"}, {"install", "--quiet"}} {
		out, _, err := runFont(t, env, args...)
		if err != nil {
			t.Fatalf("%v: %v, want exit code 0", args, err)
		}
		wantLines(t, out, "• Skipped: no internet. Install later from Settings › Icon font.")
	}
	if len(*saved) != 0 {
		t.Errorf("saved %+v after an offline run", *saved)
	}
}

// TestFontInstallPolicy checks a policy block prints the manual steps,
// on one line under --quiet, with exit code 0 and nothing saved.
func TestFontInstallPolicy(t *testing.T) {
	env, saved := fakeFontEnv()
	env.deps.Install = func(context.Context, fonts.Options) (fonts.Result, error) {
		return fonts.Result{}, &fonts.PolicyError{Err: errors.New("access denied"), ManualSteps: "Ask an administrator."}
	}

	out, _, err := runFont(t, env, "install")
	if err != nil {
		t.Fatalf("install: %v, want exit code 0", err)
	}
	wantLines(t, out, "! Blocked by policy:", "  Ask an administrator.")

	out, _, err = runFont(t, env, "install", "--quiet")
	if err != nil {
		t.Fatalf("install --quiet: %v, want exit code 0", err)
	}
	wantLines(t, out, "! Blocked by policy: Ask an administrator.")
	if len(*saved) != 0 {
		t.Errorf("saved %+v after a policy block", *saved)
	}
}

// TestFontInstallUnexpectedError checks any other failure is returned (exit
// code 1, printed on stderr by Execute) and prints nothing on stdout.
func TestFontInstallUnexpectedError(t *testing.T) {
	env, saved := fakeFontEnv()
	env.deps.Install = func(context.Context, fonts.Options) (fonts.Result, error) {
		return fonts.Result{}, errors.New("fonts: checksum mismatch")
	}

	out, _, err := runFont(t, env, "install", "--quiet")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want the checksum mismatch", err)
	}
	if out != "" || len(*saved) != 0 {
		t.Errorf("stdout %q, saved %+v: want neither", out, *saved)
	}
}

// TestFontInstallConfigNotSaved checks the font is still installed when the
// config cannot be loaded or is unsafe to overwrite, and that the user is
// told the setting was not saved.
func TestFontInstallConfigNotSaved(t *testing.T) {
	for _, tc := range []struct {
		name string
		load func() (config.Result, error)
		save error
		want string
	}{
		{
			name: "load error",
			load: func() (config.Result, error) { return config.Result{}, errors.New("config: reading x: denied") },
			want: "! Setting not saved: config: reading x: denied",
		},
		{
			name: "newer schema",
			load: func() (config.Result, error) {
				return config.Result{Config: config.Default(), Warning: "Update Devpit."}, nil
			},
			want: "! Setting not saved: Update Devpit.",
		},
		{
			name: "save error",
			save: errors.New("config: replacing x: denied"),
			want: "! Setting not saved: config: replacing x: denied",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, saved := fakeFontEnv()
			installed := false
			env.deps.Install = func(context.Context, fonts.Options) (fonts.Result, error) {
				installed = true
				return fonts.Result{Installed: true}, nil
			}
			if tc.load != nil {
				env.loadConfig = tc.load
			}
			if tc.save != nil {
				env.saveConfig = func(config.Config) error { return tc.save }
			}

			out, _, err := runFont(t, env, "install")
			if err != nil {
				t.Fatalf("install: %v, want exit code 0", err)
			}
			if !installed {
				t.Fatal("the font was not installed")
			}
			if tc.save == nil && len(*saved) != 0 {
				t.Errorf("saved %+v over a config that must not be overwritten", *saved)
			}
			if !strings.HasSuffix(out, tc.want+"\n") {
				t.Errorf("output %q does not end with %q", out, tc.want)
			}
		})
	}
}

// TestFontRemove checks remove restores the Terminal, reports it, and
// saves the reset config.
func TestFontRemove(t *testing.T) {
	env, saved := fakeFontEnv()
	env.loadConfig = func() (config.Result, error) {
		cfg := config.Default()
		cfg.FontInstalled, cfg.GlyphsConfirmed, cfg.Icons = true, true, config.IconsNerd
		return config.Result{Config: cfg}, nil
	}

	out, _, err := runFont(t, env, "remove")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	wantLines(t, out,
		"✔ Icon font removed",
		"  ✔ Windows Terminal: original settings restored ("+testWTPath+")")
	if len(*saved) != 1 {
		t.Fatalf("saved %+v, want one save", *saved)
	}
	if got := (*saved)[0]; got.FontInstalled || got.GlyphsConfirmed || got.Icons != config.IconsAuto {
		t.Errorf("saved FontInstalled %v GlyphsConfirmed %v Icons %q, want false false auto",
			got.FontInstalled, got.GlyphsConfirmed, got.Icons)
	}
}

// TestFontRemoveNotInstalled checks remove with no font says so and exits 0.
func TestFontRemoveNotInstalled(t *testing.T) {
	env, _ := fakeFontEnv()
	env.deps.Status = func(fonts.Options) (fonts.State, error) { return fonts.State{}, nil }

	out, _, err := runFont(t, env, "remove")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	wantLines(t, out, "• Icon font not installed; nothing to remove")
}

// TestFontRemoveError checks a failed removal exits 1 and saves nothing.
func TestFontRemoveError(t *testing.T) {
	env, saved := fakeFontEnv()
	env.deps.Remove = func(fonts.Options) error { return errors.New("fonts: delete registry value: denied") }

	if _, _, err := runFont(t, env, "remove"); err == nil {
		t.Fatal("remove succeeded, want an error")
	}
	if len(*saved) != 0 {
		t.Errorf("saved %+v after a failed removal", *saved)
	}
}

// TestFontStatus checks both status wordings.
func TestFontStatus(t *testing.T) {
	env, _ := fakeFontEnv()
	out, _, err := runFont(t, env, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	wantLines(t, out, "✔ Icon font installed ("+testFontPath+")")

	env.deps.Status = func(fonts.Options) (fonts.State, error) { return fonts.State{}, nil }
	out, _, err = runFont(t, env, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	wantLines(t, out, "• Icon font not installed")
}

// TestFontWindowsOnly checks every subcommand is a zero-exit note off
// Windows and never reaches the engine.
func TestFontWindowsOnly(t *testing.T) {
	env, saved := fakeFontEnv()
	env.windows = false
	env.deps = setup.Deps{
		Status: func(fonts.Options) (fonts.State, error) { t.Fatal("Status called"); return fonts.State{}, nil },
		Install: func(context.Context, fonts.Options) (fonts.Result, error) {
			t.Fatal("Install called")
			return fonts.Result{}, nil
		},
		Remove: func(fonts.Options) error { t.Fatal("Remove called"); return nil },
	}

	for _, sub := range []string{"install", "remove", "status"} {
		out, _, err := runFont(t, env, sub)
		if err != nil {
			t.Fatalf("%s: %v, want exit code 0", sub, err)
		}
		wantLines(t, out, "• Icon font install is Windows-only")
	}
	if len(*saved) != 0 {
		t.Errorf("saved %+v off Windows", *saved)
	}
}

// TestFontCommandsAreNotStubs guards against the font subcommands sliding
// back to the "not implemented" stubs they replaced.
func TestFontCommandsAreNotStubs(t *testing.T) {
	root := NewRootCmd()
	font, _, err := root.Find([]string{"font"})
	if err != nil || font.Name() != "font" {
		t.Fatalf("font command not registered: %v", err)
	}
	for _, name := range []string{"install", "remove", "status"} {
		sub, _, err := font.Find([]string{name})
		if err != nil || sub.Name() != name {
			t.Fatalf("font %s not registered: %v", name, err)
		}
		if strings.Contains(sub.Short, "not implemented") {
			t.Errorf("font %s is still described as not implemented: %q", name, sub.Short)
		}
	}
}
