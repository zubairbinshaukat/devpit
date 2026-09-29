// Package setup is the one place that knows what "install the icon font"
// means end to end: install Symbols Nerd Font Mono for the current user
// (internal/fonts), add it as a fallback to every Windows Terminal
// settings.json on disk (internal/wt), and say what that means for Devpit's
// config. Settings › Icon font and `devpit font` (which web/install.ps1 runs
// right after installing devpit.exe) both call it, so the two can never
// drift apart on which terminals get patched or which config flags flip.
//
// It imports no UI package and writes no config itself: callers get an
// Outcome, render it their own way, and persist Outcome.Apply's result
// through their own save path (a tea.Cmd in the TUI, config.Save on the
// command line).
package setup

import (
	"context"
	"errors"
	"io/fs"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/fonts"
	"github.com/zubairbinshaukat/devpit/internal/wt"
)

// FallbackFont is the font family Install appends to Windows Terminal's
// font.face list. It is the TrueType family name, not fonts.FontDisplayName,
// which additionally carries " (TrueType)" because it is a registry value
// name rather than something a terminal can match a family against.
const FallbackFont = "Symbols Nerd Font Mono"

// Kind is what an Install or Remove call ended up doing. Offline and
// PolicyBlocked are Kinds rather than errors on purpose: both are expected
// on real machines (plan.md section 10), neither must fail the installer
// script, and each needs its own wording, so callers switch on Kind instead
// of unwrapping error types they would otherwise all have to know about.
type Kind int

// Outcome kinds. The zero value is deliberately not one of them, so an
// Outcome returned alongside a non-nil error can never be mistaken for a
// success.
const (
	// Installed means this call downloaded and registered the font.
	Installed Kind = iota + 1
	// AlreadyInstalled means the font was present and nothing was
	// downloaded. Terminals are still patched, since a Terminal installed
	// after the font would otherwise never get the fallback.
	AlreadyInstalled
	// Offline means the download could not reach the network at all. The
	// font is not installed; the user can retry from Settings later.
	Offline
	// PolicyBlocked means Group Policy refused the per-user registry write.
	// Outcome.ManualSteps says how to install the font by hand.
	PolicyBlocked
	// Removed means Remove unregistered and deleted the font.
	Removed
	// NotInstalled means Remove found no font to remove and touched
	// nothing, not even Windows Terminal's settings: restoring a backup
	// without a matching install would only throw away the user's later
	// edits for no gain.
	NotInstalled
)

// Terminal is what happened to one Windows Terminal settings.json.
type Terminal struct {
	Location wt.Location
	// Changed is true when the file was rewritten: the fallback was added
	// (Install) or the pre-Devpit backup was put back (Remove). It is false
	// when the fallback was already there, or when Remove found no backup.
	Changed bool
	// BackupPath is the backup Install created or found. Empty on Remove.
	BackupPath string
	// Err is why the file could not be handled. On Install the file is left
	// untouched and the user needs wt.ManualSnippet(FallbackFont) instead;
	// the font itself is installed either way, so this never fails the run.
	Err error
}

// Outcome reports what Install or Remove did.
type Outcome struct {
	Kind Kind
	// FontPath is the installed TTF for Installed and AlreadyInstalled.
	FontPath string
	// ManualSteps is set for PolicyBlocked: text written to be shown
	// verbatim.
	ManualSteps string
	// Terminals holds one entry per Windows Terminal settings.json found,
	// in wt.FindSettings order. Empty means no Windows Terminal was found
	// (or, for Offline, PolicyBlocked and NotInstalled, none was looked at).
	Terminals []Terminal
}

// OK reports whether the run left the machine in the state the user asked
// for, i.e. whether Apply's result should be saved.
func (o Outcome) OK() bool {
	switch o.Kind {
	case Installed, AlreadyInstalled, Removed, NotInstalled:
		return true
	default:
		return false
	}
}

// Apply returns cfg updated to match the outcome. Installing only records
// that the font is there: installed is not the same as visible (Windows
// Terminal only loads a new font once every window is closed), so the icon
// tier is left alone for the glyph probe to decide. Removing also forgets
// the probe's Yes and puts the tier back to auto, since the glyphs that Yes
// was about have just gone. NotInstalled only clears the flag, so a user
// whose own terminal font has the glyphs keeps the tier they confirmed.
// Every other Kind returns cfg unchanged.
func (o Outcome) Apply(cfg config.Config) config.Config {
	switch o.Kind {
	case Installed, AlreadyInstalled:
		cfg.FontInstalled = true
	case Removed:
		cfg.FontInstalled = false
		cfg.GlyphsConfirmed = false
		cfg.Icons = config.IconsAuto
	case NotInstalled:
		cfg.FontInstalled = false
	}
	return cfg
}

// Deps are the engine calls Install, Remove and Status make. Production
// leaves every field nil, which means the real internal/fonts and
// internal/wt functions; tests fill in fakes so no test downloads a font,
// writes the real registry, or patches a real Windows Terminal settings
// file. Each field has exactly the signature of the function it stands in
// for, so wiring the real one is a plain assignment.
type Deps struct {
	Status        func(fonts.Options) (fonts.State, error)
	Install       func(context.Context, fonts.Options) (fonts.Result, error)
	Remove        func(fonts.Options) error
	FindTerminals func(wt.FindOptions) []wt.Location
	Patch         func(path, fallback string) (wt.PatchResult, error)
	Restore       func(path string) error
}

// withDefaults fills every nil field with the real implementation, so a
// caller that fakes only one call (or none) still gets a working Deps.
func (d Deps) withDefaults() Deps {
	if d.Status == nil {
		d.Status = fonts.Status
	}
	if d.Install == nil {
		d.Install = fonts.Install
	}
	if d.Remove == nil {
		d.Remove = fonts.Remove
	}
	if d.FindTerminals == nil {
		d.FindTerminals = wt.FindSettings
	}
	if d.Patch == nil {
		d.Patch = wt.Patch
	}
	if d.Restore == nil {
		d.Restore = wt.Restore
	}
	return d
}

// Install installs the font (a no-op download-wise when it is already
// there), then adds FallbackFont to every Windows Terminal settings.json it
// can find. Offline and policy-blocked installs come back as an Outcome
// with a nil error; the error is reserved for failures nobody should
// expect (a checksum mismatch, a full disk, a non-2xx download), which the
// caller should report as such.
//
// A settings.json that cannot be patched is recorded in its Terminal entry
// rather than returned: the font is installed and usable in any terminal
// the user points at it, so one unparsable file must not turn a working
// install into a failure.
func Install(ctx context.Context, deps Deps) (Outcome, error) {
	deps = deps.withDefaults()

	res, err := deps.Install(ctx, fonts.Options{})
	if err != nil {
		var offErr *fonts.OfflineError
		var polErr *fonts.PolicyError
		switch {
		case errors.As(err, &offErr):
			return Outcome{Kind: Offline}, nil
		case errors.As(err, &polErr):
			return Outcome{Kind: PolicyBlocked, ManualSteps: polErr.ManualSteps}, nil
		default:
			return Outcome{}, err
		}
	}

	out := Outcome{Kind: AlreadyInstalled, FontPath: res.FontPath}
	if res.Installed {
		out.Kind = Installed
	}
	for _, loc := range deps.FindTerminals(wt.FindOptions{}) {
		pr, perr := deps.Patch(loc.Path, FallbackFont)
		out.Terminals = append(out.Terminals, Terminal{
			Location:   loc,
			Changed:    perr == nil && pr.Changed,
			BackupPath: pr.BackupPath,
			Err:        perr,
		})
	}
	return out, nil
}

// Remove undoes Install: it puts every Windows Terminal settings.json Devpit
// backed up back the way it was, then unregisters and deletes the font.
// Terminals are restored first so that a font removal that fails halfway
// never leaves a Terminal pointing at a fallback Devpit then forgets about.
//
// A font that is not installed is reported as NotInstalled without touching
// anything; fonts.Remove would otherwise fail on the RemoveFontResourceW of
// a file that is not there and turn "nothing to do" into an error.
//
// A settings.json with no backup is not an error (Devpit never patched it);
// any other restore failure is recorded in its Terminal entry, because the
// font removal it precedes is still worth doing.
func Remove(deps Deps) (Outcome, error) {
	deps = deps.withDefaults()

	st, err := deps.Status(fonts.Options{})
	if err != nil {
		return Outcome{}, err
	}
	if !st.Installed {
		return Outcome{Kind: NotInstalled}, nil
	}

	out := Outcome{Kind: Removed}
	for _, loc := range deps.FindTerminals(wt.FindOptions{}) {
		t := Terminal{Location: loc}
		switch rerr := deps.Restore(loc.Path); {
		case rerr == nil:
			t.Changed = true
		case errors.Is(rerr, fs.ErrNotExist):
			// No backup: Devpit never changed this file.
		default:
			t.Err = rerr
		}
		out.Terminals = append(out.Terminals, t)
	}

	if err := deps.Remove(fonts.Options{}); err != nil {
		return Outcome{}, err
	}
	return out, nil
}

// Status reports whether the font is installed. It exists so callers that
// already hold a Deps (and tests that fake it) never reach past it to the
// real fonts.Status.
func Status(deps Deps) (fonts.State, error) {
	return deps.withDefaults().Status(fonts.Options{})
}
