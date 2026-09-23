// Package config owns Devpit's on-disk preferences.
//
// The file lives at %APPDATA%\devpit\config.toml and is the only mutable
// state the program keeps between runs. Loading is a single small file read:
// it never execs a process and never touches the network.
//
// Two environment variables override the locations, for tests and for power
// users who keep a portable install:
//
//	DEVPIT_CONFIG_DIR  overrides %APPDATA%\devpit
//	DEVPIT_CACHE_DIR   overrides %LOCALAPPDATA%\devpit
package config

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// SchemaVersion is the current config schema. Migrations are forward-only:
// a file with a lower version is upgraded in memory on load, a file with a
// higher version is refused rather than silently downgraded.
const SchemaVersion = 1

// FileName is the base name of the config file inside the config directory.
const FileName = "config.toml"

// Environment variables that relocate the config and cache directories.
const (
	EnvConfigDir = "DEVPIT_CONFIG_DIR"
	EnvCacheDir  = "DEVPIT_CACHE_DIR"
)

// Theme values accepted by Config.Theme. Auto, Dark and Light pick a
// background and keep the default aqua accent; Aqua, Blue, Rose and Mono pick
// an accent and follow the terminal's own background. They mirror the
// presets in internal/ui/theme, which owns what each one looks like.
const (
	ThemeAuto  = "auto"
	ThemeDark  = "dark"
	ThemeLight = "light"
	ThemeAqua  = "aqua"
	ThemeBlue  = "blue"
	ThemeRose  = "rose"
	ThemeMono  = "mono"
)

// Themes is every accepted Config.Theme value, in the order Settings cycles
// them.
var Themes = []string{ThemeAuto, ThemeDark, ThemeLight, ThemeAqua, ThemeBlue, ThemeRose, ThemeMono}

// Icon tier values accepted by Config.Icons. They mirror icons.Tier but are
// kept as plain strings so this package does not depend on the UI.
const (
	IconsAuto    = "auto"
	IconsNerd    = "nerd"
	IconsUnicode = "unicode"
	IconsASCII   = "ascii"
)

// RecentFoldersLimit is how many recently scanned folders are remembered.
const RecentFoldersLimit = 10

// Config is the full set of user preferences. Field order matches the order
// they are written to TOML.
type Config struct {
	// Version is the schema version of the file.
	Version int `toml:"version"`

	// DefaultProjectsFolder is the folder Project Junk scans by default.
	DefaultProjectsFolder string `toml:"default_projects_folder"`
	// RecentFolders is a most-recent-first list of scanned folders.
	RecentFolders []string `toml:"recent_folders"`
	// PreferredManager is the package manager used for installs and updates.
	PreferredManager string `toml:"preferred_manager"`
	// NeverTouch is a list of absolute paths that are always skipped, both by
	// the scanner and by the delete pre-flight.
	NeverTouch []string `toml:"never_touch"`

	// Theme is one of the Theme* values (see Themes).
	Theme string `toml:"theme"`
	// Icons is one of IconsAuto, IconsNerd, IconsUnicode or IconsASCII.
	Icons string `toml:"icons"`
	// Emoji allows emoji in free-text spots (header, summaries, first run).
	Emoji bool `toml:"emoji"`
	// FontInstalled records that Devpit installed Symbols Nerd Font Mono.
	FontInstalled bool `toml:"font_installed"`
	// GlyphsConfirmed records that the user saw Nerd Font glyphs render in
	// this terminal. It is the only thing that turns the nerd tier on under
	// Icons = auto: an installed font is not proof the terminal can draw it.
	GlyphsConfirmed bool `toml:"glyphs_confirmed"`

	// TelemetryOptIn is false unless the user explicitly turns stats on.
	TelemetryOptIn bool `toml:"telemetry_opt_in"`
	// InstallID is a random UUID minted on this machine by Normalize. It is
	// the only thing a usage-stats report carries that is stable between
	// runs, and it identifies nothing else: it is not derived from the
	// machine, the user, the disk or the network, so it cannot be tied back
	// to a person even by the machine that generated it. Deleting the config
	// file mints a new one.
	InstallID string `toml:"install_id"`

	// ActiveDays is how recently a project must have been touched to count as
	// active. Active projects are never pre-ticked.
	ActiveDays int `toml:"active_days"`
	// OlderDays drives the "older than" filter on the results screen.
	OlderDays int `toml:"older_days"`
	// DevPorts is the list of ports the busy-dev-ports view checks.
	DevPorts []int `toml:"dev_ports"`

	// LifetimeFreedBytes is the running total of space freed by Devpit.
	LifetimeFreedBytes uint64 `toml:"lifetime_freed_bytes"`
	// FirstRunDone is set once the first-run screen has been completed.
	FirstRunDone bool `toml:"first_run_done"`
}

// DefaultDevPorts is the recommended dev port list from the implementation
// plan: 3000-3010, 4200, 5000, 5173, 5174, 8000, 8080, 8081, 8888, 9000 and
// the Expo range 19000-19006.
func DefaultDevPorts() []int {
	ports := make([]int, 0, 27)
	for p := 3000; p <= 3010; p++ {
		ports = append(ports, p)
	}
	ports = append(ports, 4200, 5000, 5173, 5174, 8000, 8080, 8081, 8888, 9000)
	for p := 19000; p <= 19006; p++ {
		ports = append(ports, p)
	}
	return ports
}

// Default returns the configuration a fresh install starts with.
func Default() Config {
	return Config{
		Version:       SchemaVersion,
		RecentFolders: []string{},
		NeverTouch:    []string{},
		Theme:         ThemeAuto,
		Icons:         IconsAuto,
		Emoji:         true,
		ActiveDays:    7,
		OlderDays:     30,
		DevPorts:      DefaultDevPorts(),
	}
}

// Result is what Load returns: the configuration plus anything the user should
// be told about how it was obtained.
type Result struct {
	// Config is always usable, even when loading failed.
	Config Config
	// Path is the file the config was read from or will be written to.
	Path string
	// Created is true when no file existed and defaults were used.
	Created bool
	// Warning is non-empty when the file on disk was unusable. It is written
	// for a human and is shown once, then dismissed.
	Warning string
}

// Dir returns the directory holding config.toml, creating nothing.
// DEVPIT_CONFIG_DIR wins; otherwise %APPDATA%\devpit via os.UserConfigDir.
func Dir() (string, error) {
	if d := os.Getenv(EnvConfigDir); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locating the config directory: %w", err)
	}
	return filepath.Join(base, "devpit"), nil
}

// CacheDir returns the directory for caches and logs, creating nothing.
// DEVPIT_CACHE_DIR wins; otherwise %LOCALAPPDATA%\devpit via os.UserCacheDir.
func CacheDir() (string, error) {
	if d := os.Getenv(EnvCacheDir); d != "" {
		return d, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("config: locating the cache directory: %w", err)
	}
	return filepath.Join(base, "devpit"), nil
}

// EnsureCacheDir returns the cache directory, creating it if needed.
func EnsureCacheDir() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("config: creating %s: %w", dir, err)
	}
	return dir, nil
}

// Path returns the full path to config.toml.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Load reads the config file. It never returns an unusable Config: if the file
// is missing, defaults are returned with Created set; if the file is corrupt
// it is renamed aside to config.toml.broken-<unix> and defaults are returned
// with Warning set. An error comes back only when even that recovery failed.
func Load() (Result, error) {
	path, err := Path()
	if err != nil {
		return Result{Config: Default()}, err
	}
	res := Result{Config: Default(), Path: path}

	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		res.Created = true
		return res, nil
	case err != nil:
		return res, fmt.Errorf("config: reading %s: %w", path, err)
	}

	var cfg Config
	if uerr := toml.Unmarshal(data, &cfg); uerr != nil {
		broken, rerr := quarantine(path)
		if rerr != nil {
			res.Warning = fmt.Sprintf(
				"Your settings file at %s could not be read and could not be moved aside, so Devpit is using defaults for this run. Read error: %v. Move error: %v.",
				path, uerr, rerr)
			return res, nil
		}
		res.Warning = fmt.Sprintf(
			"Your settings file could not be read, so it was moved to %s and Devpit started fresh. Nothing else was changed.",
			filepath.Base(broken))
		res.Created = true
		return res, nil
	}

	if cfg.Version > SchemaVersion {
		res.Warning = fmt.Sprintf(
			"Your settings file uses schema version %d but this Devpit understands %d. Update Devpit, or delete %s to start fresh. Defaults are in use for this run.",
			cfg.Version, SchemaVersion, path)
		return res, nil
	}

	cfg.Normalize()
	res.Config = cfg
	return res, nil
}

// Save writes the config atomically: a temp file in the same directory, then a
// rename over the target. A half-written file is therefore never observable.
func Save(cfg Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	cfg.Normalize()

	dir := filepath.Dir(path)
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return fmt.Errorf("config: creating %s: %w", dir, mkErr)
	}

	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: encoding settings: %w", err)
	}

	tmp, err := os.CreateTemp(dir, FileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("config: creating a temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Harmless once the rename below has succeeded.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: writing %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: flushing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: closing %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: replacing %s: %w", path, err)
	}
	return nil
}

// Normalize repairs a partially filled config in place: it clamps enum fields
// to known values, restores sane numbers and replaces nil slices with empty
// ones so TOML round-trips cleanly.
func (c *Config) Normalize() {
	c.Version = SchemaVersion

	switch c.Theme {
	case ThemeAuto, ThemeDark, ThemeLight, ThemeAqua, ThemeBlue, ThemeRose, ThemeMono:
	default:
		c.Theme = ThemeAuto
	}
	switch c.Icons {
	case IconsAuto, IconsNerd, IconsUnicode, IconsASCII:
	default:
		c.Icons = IconsAuto
	}
	if c.ActiveDays <= 0 {
		c.ActiveDays = 7
	}
	if c.OlderDays <= 0 {
		c.OlderDays = 30
	}
	if len(c.DevPorts) == 0 {
		c.DevPorts = DefaultDevPorts()
	}
	if c.RecentFolders == nil {
		c.RecentFolders = []string{}
	}
	if c.NeverTouch == nil {
		c.NeverTouch = []string{}
	}
	if c.InstallID == "" {
		c.InstallID = NewInstallID()
	}
}

// NewInstallID returns a random UUID v4, or "" if the system's random source
// is unavailable — in which case the caller simply has no install ID, which
// is a report the server drops rather than a reason to fail a save.
func NewInstallID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// AddRecentFolder pushes path to the front of the recent list, dropping any
// case-insensitive duplicate and keeping at most RecentFoldersLimit entries.
func (c *Config) AddRecentFolder(path string) {
	if path == "" {
		return
	}
	out := make([]string, 0, RecentFoldersLimit)
	out = append(out, path)
	for _, p := range c.RecentFolders {
		if len(out) >= RecentFoldersLimit {
			break
		}
		if !strings.EqualFold(p, path) {
			out = append(out, p)
		}
	}
	c.RecentFolders = out
}

// quarantine renames a bad config file aside and returns the new path.
func quarantine(path string) (string, error) {
	broken := fmt.Sprintf("%s.broken-%d", path, time.Now().Unix())
	if err := os.Rename(path, broken); err != nil {
		return "", err
	}
	return broken, nil
}
