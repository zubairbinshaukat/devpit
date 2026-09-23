// Package telemetry is Devpit's opt-in usage-stats client, and the whole of
// it is written to make PRIVACY.md true rather than merely plausible.
//
// One report goes out after a cleanup that actually removed something. It
// carries numbers and short labels only: bytes freed, how many items, the
// rule names those items matched, the OS name and version, the Devpit version
// and a random install ID. No path, no file or project name, no user name and
// no machine name ever reaches [Build], because [Build] only ever reads counts
// and rule names — a rule name that does not look like a rule name is dropped
// rather than sent.
//
// Everything here is best-effort. [Send] bounds itself with a three second
// timeout, ignores the response, retries nothing and queues nothing to disk.
// Callers run it on a goroutine and discard the error; the error exists so a
// test can see what the user never does.
//
// The package has no mutable package-level state, no init and no dependency
// on the terminal UI.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/clean"
	"github.com/zubairbinshaukat/devpit/internal/config"
)

// DefaultEndpoint is where a report is posted. The server answers 204 and the
// client ignores the body.
const DefaultEndpoint = "https://devpit.zubyr.dev/api/report"

// DefaultTimeout bounds a whole report: connect, write and read together.
const DefaultTimeout = 3 * time.Second

// WireVersion is the schema of the JSON body, sent as "v".
const WireVersion = 1

// Environment variables that switch usage stats off whatever the setting
// says. Either one wins over an opt-in.
const (
	EnvNoTelemetry = "DEVPIT_NO_TELEMETRY"
	EnvDoNotTrack  = "DO_NOT_TRACK"
)

// Limits on the parts of a report that come from rule names and from the
// operating system, so nothing unbounded or unexpected is ever posted.
const (
	// MaxTypes is how many distinct rule names one report may carry.
	MaxTypes = 32
	// MaxTypeLen is the longest rule name that is kept; longer ones are
	// dropped, never truncated, because a truncated name is a guess.
	MaxTypeLen = 40
	// MaxOSVersionLen bounds the OS version string.
	MaxOSVersionLen = 32
)

// Report is the exact JSON body that is posted. Field order here is the field
// order on the wire, and there are no other fields: adding one is a change to
// PRIVACY.md first.
type Report struct {
	// V is [WireVersion].
	V int `json:"v"`
	// InstallID is the random UUID from the config file.
	InstallID string `json:"installId"`
	// Version is the Devpit version, e.g. "1.2.3" or "dev".
	Version string `json:"version"`
	// OS is runtime.GOOS.
	OS string `json:"os"`
	// OSVersion is "major.minor.build" on Windows and "" elsewhere.
	OSVersion string `json:"osVersion"`
	// FreedBytes is clean.Report.Freed.
	FreedBytes uint64 `json:"freedBytes"`
	// Items is how many items were removed.
	Items int `json:"items"`
	// Types counts removed items per rule name. The counts sum to at most
	// Items, and may sum to less when a rule name was dropped.
	Types map[string]int `json:"types"`
}

// Options configures a [Client]. The zero value is usable: it posts to
// [DefaultEndpoint] with a [DefaultTimeout] client and an empty version and
// install ID, which is what a test that only cares about the shape wants.
type Options struct {
	// Endpoint overrides [DefaultEndpoint].
	Endpoint string
	// HTTPClient overrides the default client. Its Timeout, when set, also
	// bounds the context [Send] builds, so a test can shorten both at once.
	HTTPClient *http.Client
	// Version is the Devpit version string, normally version.Short().
	Version string
	// InstallID is config.Config.InstallID.
	InstallID string
	// OSInfo resolves the OS name and version. It is a field so a test can
	// inject a fixed pair instead of asking the running kernel; nil means
	// [SystemInfo].
	OSInfo func() (name, version string)
}

// Client posts reports. It is immutable once built, so it is safe to share
// between goroutines.
type Client struct {
	endpoint  string
	http      *http.Client
	timeout   time.Duration
	version   string
	installID string
	osName    string
	osVersion string
}

// New returns a client with every unset option filled in with its default.
func New(opts Options) *Client {
	c := &Client{
		endpoint:  opts.Endpoint,
		http:      opts.HTTPClient,
		version:   opts.Version,
		installID: opts.InstallID,
	}
	if c.endpoint == "" {
		c.endpoint = DefaultEndpoint
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: DefaultTimeout}
	}
	c.timeout = c.http.Timeout
	if c.timeout <= 0 {
		c.timeout = DefaultTimeout
	}

	osInfo := opts.OSInfo
	if osInfo == nil {
		osInfo = SystemInfo
	}
	name, ver := osInfo()
	c.osName = name
	c.osVersion = sanitizeOSVersion(ver)
	return c
}

// Endpoint is where this client posts, exposed for tests and for the
// diagnostics screen.
func (c *Client) Endpoint() string { return c.endpoint }

// Enabled reports whether a report may be sent at all.
//
// It is true only when the user opted in and neither kill switch is set. A
// kill switch counts as set for any value except "", "0" and "false"
// (case-insensitive, surrounding spaces ignored), so `DO_NOT_TRACK=1`,
// `=true` and `=yes` all stop reports while `DO_NOT_TRACK=0` does not — the
// unset-like values are spelled out rather than guessed at.
func Enabled(cfg config.Config, getenv func(string) string) bool {
	if !cfg.TelemetryOptIn {
		return false
	}
	if getenv == nil {
		return true
	}
	return !envSet(getenv(EnvNoTelemetry)) && !envSet(getenv(EnvDoNotTrack))
}

// envSet reports that an environment variable asks for something, rather than
// merely existing.
func envSet(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false":
		return false
	default:
		return true
	}
}

// Build turns a finished cleanup into the body to post.
//
// rules maps an item's path to the scan rule that matched it; the paths are
// looked up and thrown away, and only the rule names can reach the report.
// Keys are matched case-insensitively, which is what Windows paths need.
//
// ok is false when there is nothing to report — nothing deleted and nothing
// freed, which is what a cancelled run or an all-skipped run looks like. A
// caller that honours ok therefore sends nothing on cancel, which is the
// PRIVACY.md promise.
func (c *Client) Build(rep clean.Report, rules map[string]string) (Report, bool) {
	if len(rep.Deleted) == 0 && rep.Freed == 0 {
		return Report{}, false
	}

	types := make(map[string]int, len(rules))
	for _, r := range rep.Deleted {
		name, ok := cleanTypeKey(rules[strings.ToLower(r.Path)])
		if !ok {
			continue
		}
		if _, seen := types[name]; !seen && len(types) >= MaxTypes {
			// Over the cap: drop the whole key rather than send a partial
			// one. Counts then sum to less than Items, which is allowed.
			continue
		}
		types[name]++
	}

	return Report{
		V:          WireVersion,
		InstallID:  c.installID,
		Version:    c.version,
		OS:         c.osName,
		OSVersion:  c.osVersion,
		FreedBytes: rep.Freed,
		Items:      len(rep.Deleted),
		Types:      types,
	}, true
}

// cleanTypeKey lower-cases a rule name and accepts it only if it is a rule
// name: [a-z0-9._-] and at most [MaxTypeLen]. A path fails on its separator
// or its colon, so a bug that passed a path here sends nothing rather than
// leaking one.
func cleanTypeKey(rule string) (string, bool) {
	rule = strings.ToLower(strings.TrimSpace(rule))
	if rule == "" || len(rule) > MaxTypeLen {
		return "", false
	}
	for _, r := range rule {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return "", false
		}
	}
	return rule, true
}

// sanitizeOSVersion keeps the version to the characters the wire contract
// allows and to [MaxOSVersionLen], dropping anything else. A kernel that
// reported something odd cannot widen what is sent.
func sanitizeOSVersion(v string) string {
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r == '.', r == ' ', r == '_', r == '-':
			if b.Len() >= MaxOSVersionLen {
				return b.String()
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Send posts one report and forgets it.
//
// The response is ignored entirely, status code included: a 500 is as final
// as a 204, because there is no retry and no queue. An error comes back only
// when the request could not be made or completed — a closed port, no network,
// or the timeout — and callers in the app discard it. Nothing here writes to
// disk and nothing here can block longer than the client's timeout.
func (c *Client) Send(ctx context.Context, rep Report) error {
	body, err := json.Marshal(rep)
	if err != nil {
		return fmt.Errorf("telemetry: encoding the report: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telemetry: building the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("telemetry: posting the report: %w", err)
	}
	// The body is not read at all; closing it is all this client owes it.
	return resp.Body.Close()
}
