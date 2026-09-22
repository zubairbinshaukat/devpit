package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/config"
)

// withTempDirs points the config and cache directories at a fresh temp dir so
// no test can read or write the developer's real settings.
func withTempDirs(t *testing.T) (cfgDir, cacheDir string) {
	t.Helper()
	cfgDir = t.TempDir()
	cacheDir = t.TempDir()
	t.Setenv(config.EnvConfigDir, cfgDir)
	t.Setenv(config.EnvCacheDir, cacheDir)
	return cfgDir, cacheDir
}

func TestDirsFollowEnvOverrides(t *testing.T) {
	cfgDir, cacheDir := withTempDirs(t)

	got, err := config.Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if got != cfgDir {
		t.Errorf("Dir = %q, want %q", got, cfgDir)
	}

	got, err = config.CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	if got != cacheDir {
		t.Errorf("CacheDir = %q, want %q", got, cacheDir)
	}

	p, err := config.Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := filepath.Join(cfgDir, config.FileName); p != want {
		t.Errorf("Path = %q, want %q", p, want)
	}
}

func TestEnsureCacheDirCreates(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "nested", "devpit")
	t.Setenv(config.EnvCacheDir, target)

	got, err := config.EnsureCacheDir()
	if err != nil {
		t.Fatalf("EnsureCacheDir: %v", err)
	}
	if got != target {
		t.Fatalf("EnsureCacheDir = %q, want %q", got, target)
	}
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		t.Fatalf("cache dir was not created: %v", err)
	}
}

func TestDefaults(t *testing.T) {
	d := config.Default()

	if d.Version != config.SchemaVersion {
		t.Errorf("Version = %d, want %d", d.Version, config.SchemaVersion)
	}
	if d.TelemetryOptIn {
		t.Error("telemetry must be off by default")
	}
	if d.FirstRunDone {
		t.Error("first run must not be marked done by default")
	}
	if d.ActiveDays != 7 || d.OlderDays != 30 {
		t.Errorf("ActiveDays/OlderDays = %d/%d, want 7/30", d.ActiveDays, d.OlderDays)
	}
	if d.Theme != config.ThemeAuto || d.Icons != config.IconsAuto {
		t.Errorf("Theme/Icons = %q/%q, want auto/auto", d.Theme, d.Icons)
	}
	if !d.Emoji {
		t.Error("emoji should default on")
	}
}

func TestDefaultDevPorts(t *testing.T) {
	ports := config.DefaultDevPorts()
	want := map[int]bool{3000: true, 3010: true, 4200: true, 5173: true, 8080: true, 19000: true, 19006: true}
	have := make(map[int]bool, len(ports))
	for _, p := range ports {
		if have[p] {
			t.Errorf("port %d listed twice", p)
		}
		have[p] = true
	}
	for p := range want {
		if !have[p] {
			t.Errorf("default dev ports are missing %d", p)
		}
	}
	if have[3011] {
		t.Error("3011 should not be in the default range")
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	withTempDirs(t)

	res, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !res.Created {
		t.Error("Created should be true when no file exists")
	}
	if res.Warning != "" {
		t.Errorf("unexpected warning: %q", res.Warning)
	}
	if res.Config.ActiveDays != 7 {
		t.Errorf("expected defaults, got ActiveDays=%d", res.Config.ActiveDays)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	withTempDirs(t)

	in := config.Default()
	in.DefaultProjectsFolder = `D:\work`
	in.PreferredManager = "scoop"
	in.NeverTouch = []string{`D:\keep`}
	in.Theme = config.ThemeDark
	in.Icons = config.IconsNerd
	in.Emoji = false
	in.FontInstalled = true
	in.TelemetryOptIn = true
	in.ActiveDays = 14
	in.OlderDays = 60
	in.DevPorts = []int{3000, 5173}
	in.LifetimeFreedBytes = 40_802_189_312
	in.FirstRunDone = true
	in.AddRecentFolder(`D:\work\api`)

	if err := config.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	res, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out := res.Config

	if out.DefaultProjectsFolder != in.DefaultProjectsFolder {
		t.Errorf("DefaultProjectsFolder = %q, want %q", out.DefaultProjectsFolder, in.DefaultProjectsFolder)
	}
	if out.Icons != config.IconsNerd || out.Theme != config.ThemeDark {
		t.Errorf("Icons/Theme = %q/%q", out.Icons, out.Theme)
	}
	if out.Emoji {
		t.Error("Emoji should have round-tripped as false")
	}
	if !out.TelemetryOptIn || !out.FontInstalled || !out.FirstRunDone {
		t.Error("boolean fields did not round-trip")
	}
	if out.ActiveDays != 14 || out.OlderDays != 60 {
		t.Errorf("day fields = %d/%d, want 14/60", out.ActiveDays, out.OlderDays)
	}
	if out.LifetimeFreedBytes != in.LifetimeFreedBytes {
		t.Errorf("LifetimeFreedBytes = %d, want %d", out.LifetimeFreedBytes, in.LifetimeFreedBytes)
	}
	if len(out.RecentFolders) != 1 || out.RecentFolders[0] != `D:\work\api` {
		t.Errorf("RecentFolders = %v", out.RecentFolders)
	}
	if len(out.DevPorts) != 2 {
		t.Errorf("DevPorts = %v", out.DevPorts)
	}
}

func TestSaveIsAtomicAndLeavesNoTempFiles(t *testing.T) {
	dir, _ := withTempDirs(t)

	if err := config.Save(config.Default()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 || entries[0].Name() != config.FileName {
		t.Errorf("unexpected directory contents: %v", names(entries))
	}
}

func TestCorruptFileIsQuarantined(t *testing.T) {
	dir, _ := withTempDirs(t)
	path := filepath.Join(dir, config.FileName)

	if err := os.WriteFile(path, []byte("this is not [ valid toml =\n"), 0o600); err != nil {
		t.Fatalf("seeding a corrupt file: %v", err)
	}

	res, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.Warning == "" {
		t.Fatal("a corrupt file must produce a warning")
	}
	if !res.Created {
		t.Error("a quarantined file should leave Created set")
	}
	if res.Config.ActiveDays != 7 {
		t.Error("a quarantined file should leave defaults behind")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var broken int
	for _, e := range entries {
		if strings.Contains(e.Name(), ".broken-") {
			broken++
		}
		if e.Name() == config.FileName {
			t.Error("the corrupt file should have been renamed away")
		}
	}
	if broken != 1 {
		t.Errorf("expected exactly one .broken- file, got %d in %v", broken, names(entries))
	}
}

func TestFutureSchemaIsRefusedNotDowngraded(t *testing.T) {
	dir, _ := withTempDirs(t)
	path := filepath.Join(dir, config.FileName)

	if err := os.WriteFile(path, []byte("version = 99\nolder_days = 5\n"), 0o600); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	res, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.Warning == "" {
		t.Fatal("a future schema must warn")
	}
	if res.Config.OlderDays != 30 {
		t.Errorf("a future schema must not be partly applied, got OlderDays=%d", res.Config.OlderDays)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("a future-schema file must be left on disk untouched")
	}
}

func TestNormalizeRepairsJunk(t *testing.T) {
	c := config.Config{Theme: "neon", Icons: "emoji", ActiveDays: -3, OlderDays: 0}
	c.Normalize()

	if c.Version != config.SchemaVersion {
		t.Errorf("Version = %d", c.Version)
	}
	if c.Theme != config.ThemeAuto {
		t.Errorf("Theme = %q, want auto", c.Theme)
	}
	if c.Icons != config.IconsAuto {
		t.Errorf("Icons = %q, want auto", c.Icons)
	}
	if c.ActiveDays != 7 || c.OlderDays != 30 {
		t.Errorf("days = %d/%d, want 7/30", c.ActiveDays, c.OlderDays)
	}
	if len(c.DevPorts) == 0 {
		t.Error("DevPorts should have been restored")
	}
	if c.RecentFolders == nil || c.NeverTouch == nil {
		t.Error("nil slices should become empty slices")
	}
}

func TestAddRecentFolder(t *testing.T) {
	var c config.Config
	c.Normalize()

	c.AddRecentFolder(`D:\a`)
	c.AddRecentFolder(`D:\b`)
	c.AddRecentFolder(`d:\A`) // same as D:\a on Windows

	if len(c.RecentFolders) != 2 {
		t.Fatalf("RecentFolders = %v, want two entries", c.RecentFolders)
	}
	if c.RecentFolders[0] != `d:\A` {
		t.Errorf("most recent = %q", c.RecentFolders[0])
	}

	for i := range config.RecentFoldersLimit + 5 {
		c.AddRecentFolder(filepath.Join("D:", "p", string(rune('a'+i))))
	}
	if len(c.RecentFolders) > config.RecentFoldersLimit {
		t.Errorf("recent list grew to %d, limit is %d", len(c.RecentFolders), config.RecentFoldersLimit)
	}

	before := len(c.RecentFolders)
	c.AddRecentFolder("")
	if len(c.RecentFolders) != before {
		t.Error("an empty path must not be recorded")
	}
}

// names is a small helper for readable failure messages.
func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// Every theme preset Settings can cycle to must survive Normalize, or picking
// it would silently snap back to auto on the next save.
func TestEveryThemePresetSurvivesNormalize(t *testing.T) {
	t.Parallel()
	for _, name := range config.Themes {
		c := config.Default()
		c.Theme = name
		c.Normalize()
		if c.Theme != name {
			t.Errorf("Normalize rewrote theme %q to %q", name, c.Theme)
		}
	}
	c := config.Default()
	c.Theme = "orange"
	c.Normalize()
	if c.Theme != config.ThemeAuto {
		t.Errorf("an unknown theme should fall back to auto, got %q", c.Theme)
	}
}
