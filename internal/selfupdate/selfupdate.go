// Package selfupdate finds out whether a newer Devpit has been published.
//
// It asks GitHub's public releases API for the latest tag at most once every
// [Interval], caching the answer on disk, and compares it with the running
// version. It never downloads or replaces anything: Devpit is installed by a
// package manager or the install script, and each of those has its own
// upgrade command, which [Hint] names. The check is off the render path, has
// a short timeout, and any failure is swallowed, so a machine with no
// internet never notices it (PRIVACY.md, "Update checks").
package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/about"
	"github.com/zubairbinshaukat/devpit/internal/config"
)

const (
	// Endpoint is GitHub's "latest release" resource for Devpit.
	Endpoint = "https://api.github.com/repos/zubairbinshaukat/devpit/releases/latest"
	// Interval is how long a cached answer is trusted before GitHub is asked
	// again. GitHub allows sixty anonymous calls an hour per address, so a
	// tool that starts many times a day must not call on every start.
	Interval = 24 * time.Hour
	// Timeout bounds the whole request.
	Timeout = 5 * time.Second
	// CacheFile is the file under the cache directory that holds the last
	// answer.
	CacheFile = "update-check.json"
	// EnvNoCheck disables the check for anyone who would rather Devpit never
	// made the request, whatever the setting says.
	EnvNoCheck = "DEVPIT_NO_UPDATE_CHECK"
	// DevVersion is what a plain "go build" reports. A dev build is never
	// told it is out of date.
	DevVersion = "dev"
)

// Result is what a check found.
type Result struct {
	// Latest is the newest published version, without a leading "v".
	Latest string `json:"latest"`
	// URL is the release page.
	URL string `json:"url"`
	// CheckedAt is when GitHub was last asked.
	CheckedAt time.Time `json:"checked_at"`
	// Cached is true when the answer came from disk rather than GitHub.
	Cached bool `json:"-"`
}

// Options configure a check. Every field has a production default.
type Options struct {
	// Current is the running version, normally version.Short().
	Current string
	// CacheDir is where CacheFile lives. Empty means no cache: GitHub is
	// asked every time, which only tests want.
	CacheDir string
	// Endpoint overrides the GitHub URL.
	Endpoint string
	// HTTPClient overrides the client; the default carries Timeout.
	HTTPClient *http.Client
	// Now overrides the clock for tests.
	Now func() time.Time
}

// Enabled reports whether Devpit may check at all: the setting is on, the
// kill-switch variable is unset, and this is a real release build.
func Enabled(cfg config.Config, current string, getenv func(string) string) bool {
	if cfg.SkipUpdateCheck || current == "" || current == DevVersion {
		return false
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	v := strings.ToLower(strings.TrimSpace(getenv(EnvNoCheck)))
	return v == "" || v == "0" || v == "false"
}

// Check returns the latest published version, from the cache when it is
// fresh and from GitHub otherwise. A failed request with a stale cache
// returns the stale answer rather than nothing, so a flaky connection does
// not hide a notice the user already earned.
func Check(ctx context.Context, opts Options) (Result, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	cached, haveCache := readCache(opts.CacheDir)
	if haveCache && now().Sub(cached.CheckedAt) < Interval {
		cached.Cached = true
		return cached, nil
	}

	res, err := fetch(ctx, opts)
	if err != nil {
		if haveCache {
			cached.Cached = true
			return cached, nil
		}
		return Result{}, err
	}
	res.CheckedAt = now()
	writeCache(opts.CacheDir, res)
	return res, nil
}

// release is the slice of GitHub's response Devpit reads.
type release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	Pre     bool   `json:"prerelease"`
}

// fetch asks GitHub. It sends nothing but the request itself: no version,
// no identifier, no telemetry of any kind.
func fetch(ctx context.Context, opts Options) (Result, error) {
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = Endpoint
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: Timeout}
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "devpit-update-check")
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("selfupdate: GitHub answered %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&rel); err != nil {
		return Result{}, err
	}
	if rel.Draft || rel.Pre {
		return Result{}, errors.New("selfupdate: latest release is not a stable one")
	}
	v := strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v")
	if _, ok := parse(v); !ok {
		return Result{}, fmt.Errorf("selfupdate: tag %q is not a version", rel.TagName)
	}
	return Result{Latest: v, URL: rel.HTMLURL}, nil
}

// readCache loads the last answer, if there is one worth trusting.
func readCache(dir string) (Result, bool) {
	if dir == "" {
		return Result{}, false
	}
	data, err := os.ReadFile(filepath.Join(dir, CacheFile))
	if err != nil {
		return Result{}, false
	}
	var res Result
	if err := json.Unmarshal(data, &res); err != nil || res.Latest == "" || res.CheckedAt.IsZero() {
		return Result{}, false
	}
	return res, true
}

// writeCache stores an answer. A failure to write is not an error anyone
// needs to hear about: the only cost is one more request tomorrow.
func writeCache(dir string, res Result) {
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	data, err := json.Marshal(res)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, CacheFile), data, 0o600)
}

// Newer reports whether latest is a higher version than current. A dev build
// or an unparsable version is never out of date, so nothing nags a developer
// running from source.
func Newer(current, latest string) bool {
	cur, ok := parse(current)
	if !ok {
		return false
	}
	lat, ok := parse(latest)
	if !ok {
		return false
	}
	for i := range cur {
		if lat[i] != cur[i] {
			return lat[i] > cur[i]
		}
	}
	return false
}

// parse reads "1.2.3", with or without a leading "v" and with any
// pre-release or build suffix dropped, into its three numbers.
func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Hint is the one command that upgrades this install, chosen from where the
// running executable lives: Scoop and winget keep their apps in folders of
// their own, and anything else came from the install script.
func Hint(exePath string) string {
	p := strings.ToLower(filepath.ToSlash(exePath))
	switch {
	case strings.Contains(p, "/scoop/apps/"):
		return "scoop update devpit"
	case strings.Contains(p, "/winget/") || strings.Contains(p, "/microsoft/winget"):
		return "winget upgrade Zubyr.Devpit"
	default:
		return "irm " + strings.TrimPrefix(about.Website, "https://") + "/install | iex"
	}
}
