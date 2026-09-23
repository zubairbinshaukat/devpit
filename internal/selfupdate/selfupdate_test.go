package selfupdate_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/selfupdate"
)

func TestNewerComparesVersions(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.2.0", "0.1.0", false},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "v1.0.1", true},
		{"1.9.0", "1.10.0", true},
		{"1.0.0", "1.0.1-rc.1", true},
		{"dev", "1.0.0", false},
		{"", "1.0.0", false},
		{"1.0.0", "", false},
		{"1.0.0", "garbage", false},
	}
	for _, c := range cases {
		if got := selfupdate.Newer(c.current, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestEnabledHonoursTheSettingTheEnvAndTheBuild(t *testing.T) {
	env := func(vals map[string]string) func(string) string {
		return func(k string) string { return vals[k] }
	}
	on := config.Default()
	off := config.Default()
	off.SkipUpdateCheck = true

	cases := []struct {
		name    string
		cfg     config.Config
		current string
		env     map[string]string
		want    bool
	}{
		{"default", on, "1.0.0", nil, true},
		{"setting off", off, "1.0.0", nil, false},
		{"env 1", on, "1.0.0", map[string]string{selfupdate.EnvNoCheck: "1"}, false},
		{"env true", on, "1.0.0", map[string]string{selfupdate.EnvNoCheck: "true"}, false},
		{"env 0", on, "1.0.0", map[string]string{selfupdate.EnvNoCheck: "0"}, true},
		{"dev build", on, "dev", nil, false},
		{"no version", on, "", nil, false},
	}
	for _, c := range cases {
		if got := selfupdate.Enabled(c.cfg, c.current, env(c.env)); got != c.want {
			t.Errorf("%s: Enabled = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHintNamesTheInstallerThatPutItThere(t *testing.T) {
	cases := []struct{ exe, want string }{
		{`C:\Users\me\scoop\apps\devpit\current\devpit.exe`, "scoop update devpit"},
		{`C:\Users\me\AppData\Local\Microsoft\WinGet\Packages\Zubyr.Devpit_x\devpit.exe`, "winget upgrade Zubyr.Devpit"},
		{`C:\Users\me\AppData\Local\Programs\devpit\devpit.exe`, "irm devpit.zubyr.dev/install | iex"},
		{``, "irm devpit.zubyr.dev/install | iex"},
	}
	for _, c := range cases {
		if got := selfupdate.Hint(c.exe); got != c.want {
			t.Errorf("Hint(%q) = %q, want %q", c.exe, got, c.want)
		}
	}
}

// releases is a stand-in for GitHub that counts how often it is asked and
// records what the request carried.
func releases(t *testing.T, body string, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("the check sent a query string: %q", r.URL.RawQuery)
		}
		if r.ContentLength > 0 {
			t.Error("the check sent a body")
		}
		if ua := r.Header.Get("User-Agent"); ua != "devpit-update-check" {
			t.Errorf("User-Agent = %q", ua)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestCheckAsksOnceADayAndCachesTheAnswer(t *testing.T) {
	srv, calls := releases(t, `{"tag_name":"v0.2.0","html_url":"https://example.test/v0.2.0"}`, http.StatusOK)
	dir := t.TempDir()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	opts := selfupdate.Options{
		Current:  "0.1.0",
		CacheDir: dir,
		Endpoint: srv.URL,
		Now:      func() time.Time { return now },
	}

	res, err := selfupdate.Check(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Latest != "0.2.0" || res.URL != "https://example.test/v0.2.0" || res.Cached {
		t.Errorf("first check = %+v", res)
	}
	if _, statErr := os.Stat(filepath.Join(dir, selfupdate.CacheFile)); statErr != nil {
		t.Errorf("no cache written: %v", statErr)
	}

	res, err = selfupdate.Check(context.Background(), opts)
	if err != nil || !res.Cached || res.Latest != "0.2.0" {
		t.Errorf("second check = %+v, %v; want the cached answer", res, err)
	}
	if calls.Load() != 1 {
		t.Errorf("GitHub was asked %d times within a day, want 1", calls.Load())
	}

	now = now.Add(selfupdate.Interval + time.Minute)
	res, err = selfupdate.Check(context.Background(), opts)
	if err != nil || res.Cached {
		t.Errorf("check after a day = %+v, %v; want a fresh answer", res, err)
	}
	if calls.Load() != 2 {
		t.Errorf("GitHub was asked %d times after a day, want 2", calls.Load())
	}
}

func TestCheckFallsBackToAStaleCache(t *testing.T) {
	dir := t.TempDir()
	good, _ := releases(t, `{"tag_name":"v0.3.0","html_url":"u"}`, http.StatusOK)
	now := time.Now()
	opts := selfupdate.Options{Current: "0.1.0", CacheDir: dir, Endpoint: good.URL, Now: func() time.Time { return now }}
	if _, err := selfupdate.Check(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	bad, _ := releases(t, `rate limited`, http.StatusForbidden)
	opts.Endpoint = bad.URL
	now = now.Add(2 * selfupdate.Interval)
	res, err := selfupdate.Check(context.Background(), opts)
	if err != nil || !res.Cached || res.Latest != "0.3.0" {
		t.Errorf("with a stale cache and a failing server = %+v, %v; want the stale answer", res, err)
	}

	// With no cache at all a failure is a failure, and says nothing.
	res, err = selfupdate.Check(context.Background(), selfupdate.Options{Current: "0.1.0", Endpoint: bad.URL})
	if err == nil || res.Latest != "" {
		t.Errorf("without a cache = %+v, %v; want an error and no version", res, err)
	}
}

func TestCheckIgnoresPreReleasesAndBadTags(t *testing.T) {
	for _, body := range []string{
		`{"tag_name":"v0.2.0-rc.1","prerelease":true}`,
		`{"tag_name":"nightly"}`,
		`{"tag_name":"v0.2.0","draft":true}`,
		`not json`,
	} {
		srv, _ := releases(t, body, http.StatusOK)
		if res, err := selfupdate.Check(context.Background(), selfupdate.Options{Current: "0.1.0", Endpoint: srv.URL}); err == nil {
			t.Errorf("%s: accepted as %+v", body, res)
		}
	}
}

func TestCheckGivesUpQuicklyOnASlowServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)

	start := time.Now()
	_, err := selfupdate.Check(context.Background(), selfupdate.Options{
		Current:    "0.1.0",
		Endpoint:   srv.URL,
		HTTPClient: &http.Client{Timeout: 150 * time.Millisecond},
	})
	if err == nil {
		t.Fatal("a slow server produced an answer")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the check waited %v for a slow server", took)
	}
}
