package elevate

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// A step that failed outright changed nothing and is forgotten; a step that
// was cut off (cancelled, timed out) may have landed, so it stays recorded
// and stop undoes it.
func TestACutOffStepStaysRecordedAndAFailedOneDoesNot(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	f.failMatch = "-NetworkCategory Private"
	s := newSession(f)
	if _, err := run(t, s, profileReq); err == nil {
		t.Fatal("the failing profile switch succeeded")
	}
	if s.state.profileIndex != 0 {
		t.Error("a profile switch that failed outright is still recorded")
	}

	stuck := newStuckShare("-NetworkCategory Private")
	s = newSession(stuck.shareFake)
	s.exec = stuck
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := s.run(ctx, profileReq, func(string, string) {}); err == nil {
		t.Fatal("the cut-off profile switch succeeded")
	}
	if s.state.profileIndex != 7 || s.state.profileOriginal != "Public" {
		t.Errorf("a cut-off profile switch was forgotten: %+v", s.state)
	}
}

// Rules the firewall script turned on before it failed are still undone.
func TestFirewallRulesTurnedOnBeforeAFailureAreRecorded(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	f.fwFail = true
	s := newSession(f)
	if _, err := run(t, s, ShareRequest{Op: ShareOpFirewall}); err == nil {
		t.Fatal("the failing firewall step succeeded")
	}
	if len(s.state.rules) != 1 || s.state.rules[0] != "FPS-SMB-In-TCP" {
		t.Errorf("rules = %v, want the one the script turned on", s.state.rules)
	}
}

// A session makes one account, and grants and shares only for that one.
func TestASessionMakesOneAccountAndSharesOnlyForIt(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	s := newSession(f)
	if _, err := run(t, s, ShareRequest{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword}); err != nil {
		t.Fatal(err)
	}
	before := len(f.commands())
	for _, r := range []ShareRequest{
		{Op: ShareOpAccount, User: "devpit-efgh", Password: goodPassword},
		{Op: ShareOpGrant, User: "devpit-efgh", Path: `D:\Games`},
		{Op: ShareOpShare, User: "devpit-efgh", Path: `D:\Games`, Name: "Games-x7k2"},
	} {
		if _, err := run(t, s, r); err == nil || !strings.Contains(err.Error(), "refused") {
			t.Errorf("%s for devpit-efgh: err = %v, want a refusal", r.Op, err)
		}
	}
	if f.hasUser("devpit-efgh") || len(f.commands()) != before {
		t.Error("a refused request had an effect")
	}
	if _, err := run(t, s, ShareRequest{Op: ShareOpShare, User: "devpit-abcd", Path: `D:\Games`, Name: "Games-x7k2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, s, ShareRequest{Op: ShareOpShare, User: "devpit-abcd", Path: `D:\Other`, Name: "Other-1"}); err == nil {
		t.Error("a second share in one session was accepted; stop would leave the first behind")
	}
}

// A cleanup replaces the session's record, so it is refused while a share of
// this session is up; the share would otherwise never be undone.
func TestCleanupIsRefusedWhileAShareIsUp(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	s := newSession(f)
	if _, err := run(t, s, profileReq); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, s, ShareRequest{Op: ShareOpCleanup, User: "devpit-zzzz"}); err == nil {
		t.Fatal("a cleanup was accepted over a live share")
	}
	if s.state.profileIndex != 7 {
		t.Error("the refused cleanup dropped the live record")
	}
}

// PowerShell also ends a '...' string at the typographic quotes ‘ ’ ‚ ‛,
// which pathPattern lets through (folder names use them). No value is ever
// quoted into a script, so such a path stays a path.
func TestTypographicQuotesInAPathCannotBreakOutOfTheScript(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	s := newSession(f)
	evil := `D:\x’; Start-Process calc; ‘‚‛`
	if err := (ShareRequest{Op: ShareOpGrant, User: "devpit-abcd", Path: evil}).Validate(); err != nil {
		t.Fatalf("the path pattern already refuses it (%v); the test needs a path that gets through", err)
	}
	for _, r := range []ShareRequest{
		{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword},
		{Op: ShareOpGrant, User: "devpit-abcd", Path: evil},
		{Op: ShareOpShare, User: "devpit-abcd", Path: evil, Name: "x-1"},
		{Op: ShareOpStop},
	} {
		if _, err := run(t, s, r); err != nil {
			t.Fatalf("%s: %v", r.Op, err)
		}
	}
	for _, c := range f.commands() {
		if strings.HasPrefix(c, "powershell:") && strings.Contains(c, "Start-Process") {
			t.Errorf("a value reached the PowerShell parser:\n%s", c)
		}
	}
	for _, argv := range f.argvs {
		if strings.HasSuffix(argv[0], "icacls.exe") && argv[1] != evil {
			t.Errorf("icacls got the path as %q, want it as one untouched argument", argv[1])
		}
	}
}

// psString's value decodes back to exactly the input.
func TestPSStringCarriesTheValueExactly(t *testing.T) {
	for _, v := range []string{"", `D:\Mes Photos\été 写真`, `it's ‘quoted’ "twice"`, "$(calc)"} {
		expr := psString(v)
		i := strings.Index(expr, "String('") + len("String('")
		j := strings.Index(expr[i:], "'")
		b, err := base64.StdEncoding.DecodeString(expr[i : i+j])
		if err != nil || string(b) != v {
			t.Errorf("psString(%q) carries %q (%v)", v, b, err)
		}
		if strings.ContainsAny(expr[i:i+j], "'‘’‚‛\"$`") {
			t.Errorf("psString(%q) = %s holds a quote or a sigil", v, expr)
		}
	}
}

// A cleanup comes from a manifest on disk. The SID in it is used only when it
// is the temporary account's or nobody's, so a cleanup can never strip a real
// user's access to a folder.
func TestCleanupNeverRemovesAnotherAccountsFolderPermission(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	f.users["someone"] = "x"
	f.sids["someone"] = "S-1-5-21-9-9-9-1001"
	s := newSession(f)
	_, err := run(t, s, ShareRequest{
		Op: ShareOpCleanup, User: "devpit-zzzz", SID: "S-1-5-21-9-9-9-1001", Path: `D:\Games`,
	})
	if err == nil || !strings.Contains(err.Error(), "belongs to someone") {
		t.Fatalf("err = %v, want a refusal naming the owner", err)
	}
	for _, c := range f.commands() {
		if strings.Contains(c, "S-1-5-21-9-9-9-1001") {
			t.Errorf("a command touched the other account's permission: %s", c)
		}
	}

	// The SID of an account that is gone maps to nobody and is removed.
	s = newSession(f)
	if _, err := run(t, s, ShareRequest{
		Op: ShareOpCleanup, User: "devpit-zzzz", SID: "S-1-5-21-1-2-3-1004", Path: `D:\Games`,
	}); err != nil {
		t.Fatalf("cleanup of an orphaned SID: %v", err)
	}
	if !strings.Contains(strings.Join(f.commands(), "\n"), "/remove:g *S-1-5-21-1-2-3-1004") {
		t.Error("the orphaned permission was not removed")
	}
}

// A folder whose path leads somewhere else (a junction, a link, an 8.3 short
// name) is refused: the name checks cannot see where it lands.
func TestAFolderThatLeadsElsewhereIsRefused(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	s := newSession(f)
	s.resolve = func(string) (string, error) { return `C:\Windows\System32\config`, nil }
	if _, err := run(t, s, ShareRequest{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword}); err != nil {
		t.Fatal(err)
	}
	before := len(f.commands())
	for _, r := range []ShareRequest{
		{Op: ShareOpGrant, User: "devpit-abcd", Path: `D:\Innocent`},
		{Op: ShareOpShare, User: "devpit-abcd", Path: `D:\Innocent`, Name: "x-1"},
	} {
		if _, err := run(t, s, r); err == nil || !strings.Contains(err.Error(), "leads to") {
			t.Errorf("%s: err = %v, want a refusal", r.Op, err)
		}
	}
	if len(f.commands()) != before {
		t.Error("a refused folder still had a command run on it")
	}
}
