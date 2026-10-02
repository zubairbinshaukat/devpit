package adapters

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// TestRealPCProbesReadOnly asks the real Vercel, Firebase, Supabase and
// Wrangler what they support and lists Wrangler's profiles. Read-only: only
// --version, --help and `wrangler auth list` run (Supabase's probe uses an
// empty temporary home). No sign-in, nothing switched. Opt in with
// DEVPIT_REAL_PROBE=1.
func TestRealPCProbesReadOnly(t *testing.T) {
	if os.Getenv("DEVPIT_REAL_PROBE") == "" {
		t.Skip("set DEVPIT_REAL_PROBE=1")
	}
	d, err := DefaultDeps("")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, a := range []Adapter{newVercel(d), newFirebase(d), newSupabase(d), newCloudflare(d)} {
		start := time.Now()
		in := a.Installed(ctx)
		c := a.Capabilities(ctx)
		t.Logf("%s: installed=%v version=%q note=%q caps=%+v (%s)", a.Tool(), in.Found, in.Version, in.Note, c, time.Since(start).Round(time.Millisecond))
	}
	start := time.Now()
	list, err := newCloudflare(d).Accounts(ctx, accounts.NewStore())
	t.Logf("cloudflare accounts = %+v err=%v (%s)", list, err, time.Since(start).Round(time.Millisecond))
	t.Logf("wrangler bindings file = %s", WranglerBindingsFile(d))
	probs, err := CloudflareDrift(d, accounts.NewStore())
	t.Logf("drift = %+v err=%v", probs, err)

	// Vercel's who-am-I pointed at an empty folder (the spike's experiment).
	empty := shortTemp(t)
	id, err := newVercel(d).WhoAmI(ctx, accounts.Account{Tool: accounts.ToolVercel, Name: "probe", Dir: empty})
	t.Logf("vercel whoami on an empty folder: state=%s err=%v", id.State(), err)
}
