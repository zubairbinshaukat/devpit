package adapters

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// A real-looking .env.local with secrets around the one line Devpit reads.
const convexEnvLocal = "# Deployment used by `npx convex dev`\r\n" +
	"CONVEX_DEPLOYMENT=dev:tame-armadillo-385 # team: acme, project: chat-app\r\n" +
	"\r\n" +
	"CONVEX_URL=https://tame-armadillo-385.convex.cloud\r\n" +
	"OPENAI_API_KEY=sk-PLANTEDsecret" + "0123456789abcdefghij\r\n" +
	"CONVEX_DEPLOY_KEY='prod:tame-armadillo-385|PLANTEDdeploykey0123456789abcdefABCDEF'\r\n" +
	"STRIPE_SECRET=PLANTEDstripe\r\n"

func TestConvexReadsOnlyTheDeploymentLine(t *testing.T) {
	dir := shortTemp(t)
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte(convexEnvLocal), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := ConvexProject(dir)
	if err != nil || !info.Found || info.Deployment != "dev:tame-armadillo-385" || info.Team != "acme" || info.Project != "chat-app" || !info.DeployKey {
		t.Fatalf("info = %+v %v", info, err)
	}
	id := info.Identity()
	b, _ := id.MarshalJSON()
	if id.Fields().Name != "project: chat-app" || id.Fields().Org != "acme" || info.Why() != "set by this project's .env.local" {
		t.Fatalf("identity = %+v", id.Fields())
	}
	for _, s := range []string{fmt.Sprintf("%+v", info), string(b), fmt.Sprintf("%+v", id.Fields())} {
		if strings.Contains(s, "PLANTED") || strings.Contains(s, "convex.cloud") {
			t.Fatalf("another value of the env file leaked: %s", s)
		}
	}
}

func TestConvexProjectWalksUpToTheProjectRoot(t *testing.T) {
	root := shortTemp(t)
	proj := filepath.Join(root, "app")
	sub := filepath.Join(proj, "convex", "functions")
	_ = os.MkdirAll(sub, 0o700)
	_ = os.WriteFile(filepath.Join(proj, ".env"), []byte("export CONVEX_DEPLOYMENT=\"prod:happy-otter-1\"\n"), 0o600)
	info, err := ConvexProject(sub)
	if err != nil || !info.Found || info.Deployment != "prod:happy-otter-1" || info.Project != "" || info.DeployKey {
		t.Fatalf("info = %+v %v", info, err)
	}
	if info.Identity().Fields().Name != "project: prod:happy-otter-1" {
		t.Fatalf("identity = %+v", info.Identity().Fields())
	}

	// A project root in between stops the walk: the parent's file belongs
	// to another project.
	_ = os.WriteFile(filepath.Join(sub, "package.json"), []byte("{}"), 0o600)
	if info, err = ConvexProject(sub); err != nil || info.Found {
		t.Fatalf("crossed a project root: %+v %v", info, err)
	}

	// Values that are not a deployment are ignored, not echoed.
	other := shortTemp(t)
	_ = os.WriteFile(filepath.Join(other, ".env.local"), []byte("CONVEX_DEPLOYMENT=sk-PLANTED"+strings.Repeat("Ab1", 12)+"\n"), 0o600)
	if info, err = ConvexProject(other); err != nil || info.Found {
		t.Fatalf("secret-looking deployment accepted: %+v", info)
	}
}

func TestConvexIsShowOnly(t *testing.T) {
	d, fake := toolDeps(t)
	fake.Missing = map[string]bool{"convex": true}
	fake.Set(FakeResponse{Stdout: "10.9.2\n"}, "npx", "--version")
	a := newConvex(d)
	if c := a.Capabilities(context.Background()); !c.ShowOnly || c.FolderRules || c.Why != "Convex picks the account per project. Devpit shows it but does not switch it." {
		t.Fatalf("caps = %+v", c)
	}
	if in := a.Installed(context.Background()); !in.Found || !strings.Contains(in.Note, "npx") {
		t.Fatalf("installed = %+v", in)
	}
	if _, err := a.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolConvex, Name: "default"}); !errors.Is(err, accounts.ErrNotSupported) {
		t.Fatalf("whoami: %v", err)
	}
	for _, c := range fake.Calls() {
		if c.Name == "npx" && len(c.Args) > 0 && c.Args[0] != "--version" {
			t.Fatalf("ran %s", Key(c.Name, c.Args...))
		}
	}
	if _, err := a.Launch(accounts.Account{Tool: accounts.ToolConvex, Name: "x"}); !errors.Is(err, accounts.ErrNotSupported) {
		t.Fatal("convex must never launch an account")
	}
}

func TestEnvOverridesOfTheFiveTools(t *testing.T) {
	want := map[accounts.Tool][]string{
		accounts.ToolVercel:     {"VERCEL_TOKEN", "VERCEL_ORG_ID"},
		accounts.ToolFirebase:   {"FIREBASE_TOKEN", "GOOGLE_APPLICATION_CREDENTIALS"},
		accounts.ToolSupabase:   {"SUPABASE_ACCESS_TOKEN"},
		accounts.ToolCloudflare: {"CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ACCOUNT_ID"},
		accounts.ToolConvex:     {"CONVEX_DEPLOY_KEY"},
	}
	for tool, names := range want {
		got := map[string]bool{}
		for _, v := range EnvOverridesFor(tool) {
			got[v.Name] = true
			if v.Managed || v.Effect == "" {
				t.Errorf("%s %s: managed=%v effect=%q", tool, v.Name, v.Managed, v.Effect)
			}
		}
		for _, n := range names {
			if !got[n] {
				t.Errorf("%s does not list %s", tool, n)
			}
		}
	}
}
