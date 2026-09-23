package telemetry_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/clean"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/telemetry"
)

// fixedOS is the OS pair every test injects, so nothing here depends on the
// kernel it runs on.
func fixedOS() (string, string) { return "windows", "10.0.26200" }

// testClient is a client with everything pinned but the endpoint.
func testClient(endpoint string, httpClient *http.Client) *telemetry.Client {
	return telemetry.New(telemetry.Options{
		Endpoint:   endpoint,
		HTTPClient: httpClient,
		Version:    "1.2.3",
		InstallID:  "4d7f2a6c-1f0e-4a0b-9b3d-2c5e8f1a6b70",
		OSInfo:     fixedOS,
	})
}

// deleted builds a report of removed items at the given paths.
func deleted(freed uint64, paths ...string) clean.Report {
	rep := clean.Report{Freed: freed}
	for _, p := range paths {
		rep.Deleted = append(rep.Deleted, clean.Result{Item: clean.Item{Path: p, Size: 1}})
	}
	return rep
}

func TestEnabledHonoursTheOptInAndBothKillSwitches(t *testing.T) {
	cases := []struct {
		name        string
		optIn       bool
		noTelemetry string
		doNotTrack  string
		want        bool
	}{
		{name: "off by default", optIn: false, want: false},
		{name: "opted in", optIn: true, want: true},
		{name: "opted out with the switches set", optIn: false, noTelemetry: "1", doNotTrack: "1", want: false},
		{name: "DEVPIT_NO_TELEMETRY=1 wins", optIn: true, noTelemetry: "1", want: false},
		{name: "DO_NOT_TRACK=1 wins", optIn: true, doNotTrack: "1", want: false},
		{name: "both switches set", optIn: true, noTelemetry: "1", doNotTrack: "true", want: false},
		{name: "any non-empty value counts as set", optIn: true, doNotTrack: "yes", want: false},
		{name: "a switch set to 0 is not set", optIn: true, noTelemetry: "0", doNotTrack: "0", want: true},
		{name: "a switch set to false is not set", optIn: true, noTelemetry: "false", doNotTrack: "FALSE", want: true},
		{name: "an empty switch is not set", optIn: true, noTelemetry: "", doNotTrack: "  ", want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.TelemetryOptIn = tc.optIn
			env := map[string]string{
				telemetry.EnvNoTelemetry: tc.noTelemetry,
				telemetry.EnvDoNotTrack:  tc.doNotTrack,
			}
			got := telemetry.Enabled(cfg, func(k string) string { return env[k] })
			if got != tc.want {
				t.Errorf("Enabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildReportsNothingWhenNothingWasDeleted(t *testing.T) {
	c := testClient("http://127.0.0.1:0", nil)

	cases := []struct {
		name string
		rep  clean.Report
	}{
		{name: "empty report", rep: clean.Report{}},
		{name: "cancelled before anything went", rep: clean.Report{Skipped: []clean.Result{{Item: clean.Item{Path: `D:\work\api\node_modules`}}}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := c.Build(tc.rep, nil); ok {
				t.Error("Build said there was something to send")
			}
		})
	}
}

func TestBuildNeverCarriesAPathOrAName(t *testing.T) {
	host, _ := os.Hostname()
	user := os.Getenv("USERNAME")
	if user == "" {
		user = os.Getenv("USER")
	}

	rep := deleted(9_876_543_210,
		`D:\work\`+host+`\node_modules`,
		`C:\Users\`+user+`\secret-project\target`,
		`D:\work\api\node_modules`,
	)
	rules := map[string]string{
		strings.ToLower(`D:\work\` + host + `\node_modules`):           "node_modules",
		strings.ToLower(`C:\Users\` + user + `\secret-project\target`): "cargo-target",
		strings.ToLower(`D:\work\api\node_modules`):                    "node_modules",
	}

	c := testClient("http://127.0.0.1:0", nil)
	out, ok := c.Build(rep, rules)
	if !ok {
		t.Fatal("Build refused a report with deletions in it")
	}

	body, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshalling the report: %v", err)
	}
	payload := string(body)

	banned := []string{`\`, "C:", "secret-project", "Users"}
	if host != "" {
		banned = append(banned, host)
	}
	if user != "" {
		banned = append(banned, user)
	}
	for _, b := range banned {
		if strings.Contains(payload, b) {
			t.Errorf("the payload contains %q:\n%s", b, payload)
		}
	}
	if out.Items != 3 || out.Types["node_modules"] != 2 || out.Types["cargo-target"] != 1 {
		t.Errorf("counts are wrong: items=%d types=%v", out.Items, out.Types)
	}
}

func TestBuildDropsTypeKeysThatAreNotRuleNames(t *testing.T) {
	paths := []string{"a", "b", "c", "d", "e", "f"}
	rules := map[string]string{
		"a": "node_modules",
		"b": `D:\work\api\node_modules`, // a path, not a rule name
		"c": "",                         // no rule at all
		"d": strings.Repeat("x", 41),    // too long
		"e": "Cache Dir",                // a space is not allowed
		"f": "NPM-Cache",                // lower-cased, then kept
	}

	c := testClient("http://127.0.0.1:0", nil)
	out, ok := c.Build(deleted(10, paths...), rules)
	if !ok {
		t.Fatal("Build refused a report with deletions in it")
	}

	want := map[string]int{"node_modules": 1, "npm-cache": 1}
	if fmt.Sprint(out.Types) != fmt.Sprint(want) {
		t.Errorf("Types = %v, want %v", out.Types, want)
	}
	if out.Items != len(paths) {
		t.Errorf("Items = %d, want %d", out.Items, len(paths))
	}
	if sum(out.Types) > out.Items {
		t.Errorf("the type counts sum to %d, more than %d items", sum(out.Types), out.Items)
	}
}

func TestBuildCapsTheNumberOfTypes(t *testing.T) {
	var paths []string
	rules := map[string]string{}
	for i := 0; i < telemetry.MaxTypes+9; i++ {
		p := fmt.Sprintf("p%d", i)
		paths = append(paths, p)
		rules[p] = fmt.Sprintf("rule-%02d", i)
	}

	c := testClient("http://127.0.0.1:0", nil)
	out, ok := c.Build(deleted(1, paths...), rules)
	if !ok {
		t.Fatal("Build refused a report with deletions in it")
	}
	if len(out.Types) != telemetry.MaxTypes {
		t.Errorf("len(Types) = %d, want %d", len(out.Types), telemetry.MaxTypes)
	}
	if sum(out.Types) > out.Items {
		t.Errorf("the type counts sum to %d, more than %d items", sum(out.Types), out.Items)
	}
}

func TestOSVersionIsBoundedAndPlain(t *testing.T) {
	c := telemetry.New(telemetry.Options{
		OSInfo: func() (string, string) {
			return "windows", "10.0." + strings.Repeat("9", 60) + "\n<script>"
		},
	})
	out, ok := c.Build(deleted(1, "a"), nil)
	if !ok {
		t.Fatal("Build refused a report with deletions in it")
	}
	if len(out.OSVersion) > telemetry.MaxOSVersionLen {
		t.Errorf("OSVersion is %d chars: %q", len(out.OSVersion), out.OSVersion)
	}
	if strings.ContainsAny(out.OSVersion, "<>\n") {
		t.Errorf("OSVersion kept characters it should have dropped: %q", out.OSVersion)
	}
}

func TestNewDefaultsToThePublishedEndpoint(t *testing.T) {
	if got := telemetry.New(telemetry.Options{OSInfo: fixedOS}).Endpoint(); got != telemetry.DefaultEndpoint {
		t.Errorf("Endpoint = %q, want %q", got, telemetry.DefaultEndpoint)
	}
}

func TestSendPostsExactlyTheAgreedBody(t *testing.T) {
	type received struct {
		method      string
		contentType string
		body        []byte
	}
	got := make(chan received, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- received{method: r.Method, contentType: r.Header.Get("Content-Type"), body: b}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := testClient(srv.URL, nil)
	rep, ok := c.Build(deleted(123456, "a", "b", "c", "d", "e"), map[string]string{
		"a": "node_modules", "b": "node_modules", "c": "node_modules",
		"d": "npm-cache", "e": "npm-cache",
	})
	if !ok {
		t.Fatal("Build refused a report with deletions in it")
	}
	if err := c.Send(context.Background(), rep); err != nil {
		t.Fatalf("Send: %v", err)
	}

	r := <-got
	if r.method != http.MethodPost {
		t.Errorf("method = %s, want POST", r.method)
	}
	if r.contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", r.contentType)
	}

	want := `{"v":1,"installId":"4d7f2a6c-1f0e-4a0b-9b3d-2c5e8f1a6b70","version":"1.2.3","os":"windows","osVersion":"10.0.26200","freedBytes":123456,"items":5,"types":{"node_modules":3,"npm-cache":2}}`
	if string(r.body) != want {
		t.Errorf("body =\n%s\nwant\n%s", r.body, want)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(r.body, &fields); err != nil {
		t.Fatalf("the body is not JSON: %v", err)
	}
	allowed := map[string]bool{
		"v": true, "installId": true, "version": true, "os": true,
		"osVersion": true, "freedBytes": true, "items": true, "types": true,
	}
	for k := range fields {
		if !allowed[k] {
			t.Errorf("the body carries an unknown field %q", k)
		}
	}
	if len(fields) != len(allowed) {
		t.Errorf("the body has %d fields, want %d", len(fields), len(allowed))
	}
}

func TestSendSwallowsWhateverTheServerDoes(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer slow.Close()

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer broken.Close()

	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	cases := []struct {
		name     string
		endpoint string
		timeout  time.Duration
		wantErr  bool
	}{
		{name: "a 500 is as final as a 204", endpoint: broken.URL, timeout: 3 * time.Second, wantErr: false},
		{name: "nothing listening", endpoint: closedURL, timeout: 3 * time.Second, wantErr: true},
		{name: "a server slower than the timeout", endpoint: slow.URL, timeout: 150 * time.Millisecond, wantErr: true},
		{name: "an endpoint that is not a URL", endpoint: "://nope", timeout: time.Second, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(tc.endpoint, &http.Client{Timeout: tc.timeout})
			rep, ok := c.Build(deleted(1, "a"), map[string]string{"a": "node_modules"})
			if !ok {
				t.Fatal("Build refused a report with deletions in it")
			}

			start := time.Now()
			err := c.Send(context.Background(), rep)
			elapsed := time.Since(start)

			if (err != nil) != tc.wantErr {
				t.Errorf("Send error = %v, wantErr %v", err, tc.wantErr)
			}
			if limit := tc.timeout + time.Second; elapsed > limit {
				t.Errorf("Send took %s, longer than the %s it is allowed", elapsed, limit)
			}
		})
	}
}

// sum totals a type map.
func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}
