package icons

import "strings"

// Prefs is everything [Resolve] needs from the user's configuration. It is a
// plain struct so this package stays independent of internal/config.
type Prefs struct {
	// Tier is the configured preference, usually TierAuto.
	Tier Tier
	// FontInstalled is true once Devpit installed Symbols Nerd Font Mono. It
	// is informational: on its own it never turns the nerd tier on, because
	// Windows Terminal only picks a new font up after every window is
	// closed, and a tier the terminal cannot draw is a screen full of boxes.
	FontInstalled bool
	// GlyphsConfirmed is true when the user answered Yes to a glyph probe
	// (first run, or Settings > Icon check). It is the only way into the
	// nerd tier under TierAuto.
	GlyphsConfirmed bool
	// ForceASCII reflects the --ascii flag.
	ForceASCII bool
}

// Environ reads an environment variable. Pass os.Getenv in production; tests
// pass a map lookup so they never touch the real environment.
type Environ func(key string) string

// MapEnv turns a map into an [Environ], for tests.
func MapEnv(m map[string]string) Environ {
	return func(k string) string { return m[k] }
}

// Resolve applies the auto rule from the implementation plan and returns the
// icon set to use.
//
// An explicit tier in the config always wins. Otherwise, in order:
//
//  1. ascii when --ascii was passed, TERM=dumb, or NO_COLOR is set on a
//     terminal that is not Windows Terminal;
//  2. nerd when the user confirmed the glyph probe;
//  3. unicode when WT_SESSION or TERM says there is a real terminal;
//  4. ascii otherwise, which is the safe answer for bare conhost.
func Resolve(p Prefs, env Environ) Set {
	if env == nil {
		env = func(string) string { return "" }
	}

	switch p.Tier {
	case TierNerd, TierUnicode, TierASCII:
		return For(p.Tier)
	}

	term := env("TERM")
	wt := env("WT_SESSION")
	_, noColor := lookupPresent(env, "NO_COLOR")

	if p.ForceASCII || strings.EqualFold(term, "dumb") || (noColor && wt == "") {
		return ASCII()
	}
	if p.GlyphsConfirmed {
		return Nerd()
	}
	if wt != "" || term != "" {
		return Unicode()
	}
	return ASCII()
}

// lookupPresent reports whether a variable is set to a non-empty value. The
// NO_COLOR convention treats any non-empty value as "on".
func lookupPresent(env Environ, key string) (string, bool) {
	v := env(key)
	return v, v != ""
}
