package clean_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	cleanengine "github.com/zubairbinshaukat/devpit/internal/clean"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/scan"
	"github.com/zubairbinshaukat/devpit/internal/telemetry"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/clean"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// sentReport is one call the screen made to the usage-stats client.
type sentReport struct {
	cfg   config.Config
	rep   cleanengine.Report
	rules map[string]string
}

// reportSpy stands in for the real client. It records rather than posts, so
// no test here can reach the network.
type reportSpy struct {
	mu   sync.Mutex
	sent []sentReport
}

func (s *reportSpy) fn() clean.ReportFunc {
	return func(_ context.Context, cfg config.Config, rep cleanengine.Report, rules map[string]string) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.sent = append(s.sent, sentReport{cfg: cfg, rep: rep, rules: rules})
		return nil
	}
}

func (s *reportSpy) calls() []sentReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sentReport(nil), s.sent...)
}

// deletingEngines is the fake wiring for a cleanup that removes everything it
// is given, with the spy and a controlled environment attached.
func deletingEngines(spy *reportSpy, env map[string]string, items ...scan.Item) clean.Engines {
	e := baseEngines(items...)
	e.Clean = func(_ context.Context, given []cleanengine.Item, _ cleanengine.Options, _ func(cleanengine.Progress)) cleanengine.Report {
		rep := cleanengine.Report{Elapsed: time.Second}
		for _, it := range given {
			rep.Deleted = append(rep.Deleted, cleanengine.Result{Item: it})
			rep.Freed += it.Size
		}
		return rep
	}
	e.Report = spy.fn()
	e.Getenv = func(k string) string { return env[k] }
	return e
}

// runCleanup drives a whole cleanup with the given configuration.
func runCleanup(t *testing.T, engines clean.Engines, cfg config.Config) *harness {
	t.Helper()
	screen := clean.New().WithEngines(engines).WithClock(stepClock())
	h := newHarness(t, screen, cfg)

	h.special(keyEnter) // "Project Junk"
	h.settle()
	if got := h.state(); got != "results" {
		t.Fatalf("the screen is on %q after a scan, want results", got)
	}
	h.key("d")
	h.settle()
	h.key("y")
	h.settle()
	if got := h.state(); got != "summary" {
		t.Fatalf("the screen is on %q after a delete, want summary", got)
	}
	return h
}

// optedIn is a settled configuration with usage stats turned on.
func optedIn() config.Config {
	cfg := testConfig()
	cfg.TelemetryOptIn = true
	cfg.InstallID = "4d7f2a6c-1f0e-4a0b-9b3d-2c5e8f1a6b70"
	return cfg
}

// TestFinishedCleanupReportsWhenStatsAreOn proves the one report PRIVACY.md
// allows goes out after a cleanup, carrying counts and rule names only.
func TestFinishedCleanupReportsWhenStatsAreOn(t *testing.T) {
	spy := &reportSpy{}
	runCleanup(t, deletingEngines(spy, nil, safeItems()...), optedIn())

	calls := spy.calls()
	if len(calls) != 1 {
		t.Fatalf("the screen sent %d reports, want 1", len(calls))
	}
	got := calls[0]
	if got.rep.Freed != 1_700_000_000 || len(got.rep.Deleted) != 2 {
		t.Errorf("reported freed=%d items=%d, want 1700000000 and 2", got.rep.Freed, len(got.rep.Deleted))
	}
	for path, rule := range got.rules {
		if rule != "node_modules" {
			t.Errorf("rule for %q = %q, want node_modules", path, rule)
		}
	}

	// The real client over the real report: what would actually leave the
	// machine carries no path, no project name and no drive letter.
	client := telemetry.New(telemetry.Options{
		Version:   "1.2.3",
		InstallID: got.cfg.InstallID,
		OSInfo:    func() (string, string) { return "windows", "10.0.26200" },
	})
	out, ok := client.Build(got.rep, got.rules)
	if !ok {
		t.Fatal("the finished cleanup built no report")
	}
	body, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshalling the report: %v", err)
	}
	for _, banned := range []string{`\`, "D:", "api", "web"} {
		if strings.Contains(string(body), banned) {
			t.Errorf("the payload contains %q:\n%s", banned, body)
		}
	}
	if out.Items != 2 || out.Types["node_modules"] != 2 {
		t.Errorf("payload = %s, want 2 node_modules items", body)
	}
}

// TestNothingIsReportedWhenStatsAreOff is the other half of safety rule 19:
// the default configuration and either kill switch each stop the report
// before it is built.
func TestNothingIsReportedWhenStatsAreOff(t *testing.T) {
	cases := []struct {
		name  string
		optIn bool
		env   map[string]string
	}{
		{name: "off by default", optIn: false},
		{name: "DEVPIT_NO_TELEMETRY=1", optIn: true, env: map[string]string{telemetry.EnvNoTelemetry: "1"}},
		{name: "DO_NOT_TRACK=1", optIn: true, env: map[string]string{telemetry.EnvDoNotTrack: "1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &reportSpy{}
			cfg := optedIn()
			cfg.TelemetryOptIn = tc.optIn

			h := runCleanup(t, deletingEngines(spy, tc.env, safeItems()...), cfg)

			if n := len(spy.calls()); n != 0 {
				t.Fatalf("the screen sent %d reports with stats off, want 0", n)
			}
			// The cleanup itself still happened and was still persisted.
			var saved bool
			for _, msg := range h.trace {
				if c, ok := msg.(uictx.ConfigChangedMsg); ok && c.Persist {
					saved = true
				}
			}
			if !saved {
				t.Error("the cleanup did not persist its result")
			}
		})
	}
}

// TestCancelledCleanupSendsNothing pins the rule that an interrupted or
// all-skipped run leaves the machine silent, even with stats on.
func TestCancelledCleanupSendsNothing(t *testing.T) {
	spy := &reportSpy{}
	engines := deletingEngines(spy, nil, safeItems()...)
	engines.Clean = func(_ context.Context, given []cleanengine.Item, _ cleanengine.Options, _ func(cleanengine.Progress)) cleanengine.Report {
		// Cancelled before the first item: nothing removed, nothing freed.
		return cleanengine.Report{Skipped: []cleanengine.Result{{Item: given[0], Reason: "cancelled"}}}
	}

	runCleanup(t, engines, optedIn())

	calls := spy.calls()
	if len(calls) != 1 {
		t.Fatalf("the screen made %d client calls, want 1", len(calls))
	}
	client := telemetry.New(telemetry.Options{OSInfo: func() (string, string) { return "windows", "10.0.26200" }})
	if _, ok := client.Build(calls[0].rep, calls[0].rules); ok {
		t.Error("a cleanup that deleted nothing would still have posted a report")
	}
}
