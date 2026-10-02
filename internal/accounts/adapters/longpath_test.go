package adapters

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// longTemp is a temp folder whose own name is a long run of letters and
// digits, like a hashed build folder or some user profiles: exactly what the
// strict secret rule takes for a token.
func longTemp(t *testing.T) string {
	t.Helper()
	var b [20]byte
	_, _ = rand.Read(b[:])
	name := "a1" + hex.EncodeToString(b[:]) // 42 mixed letters and digits
	dir := filepath.Join(os.TempDir(), name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if !accounts.LooksSecret(name) {
		t.Fatalf("%s should look like a token to the strict rule, or this test proves nothing", name)
	}
	return dir
}

// A long, random-looking folder name works end to end: as a Git rule's
// folder (the include files, the global config's include line, real Git),
// as a Wrangler binding (the command line and the journal's Data), as
// Vercel's --global-config folder, and in the journal's summary.
func TestLongFolderNamesWorkEndToEnd(t *testing.T) {
	t.Run("git", func(t *testing.T) {
		w := newGitWorldIn(t, longTemp(t))
		w.addIdentity("work", "Zubair", "zubair@work.com")
		work := filepath.Join(w.tmp, "Work")
		repo := filepath.Join(work, "repo")
		w.initRepo(repo)
		p := w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.FolderScope(work)})
		if got := w.email(repo); got != "zubair@work.com" {
			t.Fatalf("git commits as %q in %s", got, repo)
		}
		hist, _, err := w.eng.History()
		if err != nil || len(hist) == 0 || !strings.Contains(hist[0].Summary, filepath.Base(w.tmp)) {
			t.Fatalf("the journal summary lost the folder: %+v %v", hist, err)
		}
		// The rule is written with the long name Git compares against.
		if !strings.Contains(p.Text(), filepath.ToSlash(accounts.LongPath(work))) {
			t.Fatalf("preview:\n%s", p.Text())
		}
		if _, err := w.eng.Undo(); err != nil {
			t.Fatal(err)
		}
		if got := w.email(repo); got != "base@example.invalid" {
			t.Fatalf("after undo git commits as %q", got)
		}
	})

	t.Run("wrangler", func(t *testing.T) {
		if filepath.Separator != '\\' {
			t.Skip("Wrangler's folder bindings are Windows paths here")
		}
		a, d, _, path := newCloudflareForTest(t)
		eng := accounts.NewEngine(d.Paths)
		eng.Handlers = Handlers(d)
		if _, err := eng.AddAccount(accounts.Account{Tool: accounts.ToolCloudflare, Name: "work", Email: "z@work.com"}); err != nil {
			t.Fatal(err)
		}
		site := filepath.Join(longTemp(t), "Site")
		if err := os.MkdirAll(site, 0o700); err != nil {
			t.Fatal(err)
		}
		res, _ := eng.Load()
		p, err := a.Plan(accounts.PreviewInput{Store: res.Store, Change: accounts.Change{Tool: accounts.ToolCloudflare, Account: "work", Scope: accounts.FolderScope(site)}})
		if err != nil {
			t.Fatal(err)
		}
		fin := lastEvent(drainEvents(t, Apply(context.Background(), eng, a, p)))
		if fin.State != accounts.StepDone {
			t.Fatalf("apply: %+v %v", fin, fin.Err)
		}
		if b := readBindingsForTest(t, path); b[wranglerTrueCase(site)] != "work" {
			t.Fatalf("bindings = %+v", b)
		}
		if _, err := eng.Undo(); err != nil {
			t.Fatal(err)
		}
		if b := readBindingsForTest(t, path); len(b) != 0 {
			t.Fatalf("after undo = %+v", b)
		}
	})

	t.Run("vercel", func(t *testing.T) {
		_, d, fake := newVercelForTest(t)
		d.Paths = accounts.PathsIn(shortTemp(t), filepath.Join(longTemp(t), "accounts"))
		a := newVercel(d).(*vercelAdapter)
		fake.Func = func(c Cmd) (Result, error) {
			if len(c.Args) == 3 && c.Args[0] == "--global-config" && c.Args[2] == "login" {
				return Result{Stdout: []byte("> Success!\n")}, nil
			}
			if len(c.Args) == 4 && c.Args[0] == "--global-config" && c.Args[2] == "whoami" {
				return Result{Stdout: []byte(`{"username":"zubair","email":"z@work.com"}`)}, nil
			}
			return Result{}, errors.New("no answer for " + Key(c.Name, c.Args...))
		}
		ch, err := a.Login(context.Background(), LoginRequest{Name: "work"})
		if err != nil {
			t.Fatal(err)
		}
		fin := lastEvent(drainEvents(t, ch))
		if fin.State != accounts.StepDone || fin.Account == nil {
			t.Fatalf("login: %+v %v", fin, fin.Err)
		}
		dir := d.Paths.AccountDir(accounts.ToolVercel, "work")
		if _, ok := ran(fake, "vercel", "--global-config", dir, "login"); !ok {
			t.Fatalf("vercel was not run with --global-config %s", dir)
		}
		if l, err := LaunchFor(*fin.Account); err != nil || l.Args[1] != dir {
			t.Fatalf("launch: %+v %v", l, err)
		}
	})
}

// A planted token still never gets through, wherever paths are now judged
// by the known-prefix rule.
func TestPlantedTokenStillRefusedNextToPaths(t *testing.T) {
	gh := "ghp_" + strings.Repeat("Ab1", 12)
	raw := "Zq9" + strings.Repeat("x7Y", 12) // a 39-character mixed run
	for _, args := range [][]string{
		{"auth", "activate", "work", `C:\Work\` + gh},
		{"--global-config", `C:\Users\me\.devpit\accounts\vercel\` + gh},
		{"--token=C:\\Work\\x"},
		{"login", "--token", raw},
		{"whoami", raw},
	} {
		if err := CheckArgs("vercel", args); !errors.Is(err, ErrForbiddenCommand) {
			t.Errorf("CheckArgs(%v) = %v, want refused", args, err)
		}
	}
	long := filepath.Join(`C:\Users\me\AppData\Local\Temp`, "a1"+strings.Repeat("b2", 20), "Site")
	for _, args := range [][]string{
		{"auth", "activate", "work", long},
		{"--global-config", long, "whoami", "--json"},
		{"--global-config=" + long},
	} {
		if err := CheckArgs("wrangler", args); err != nil {
			t.Errorf("CheckArgs(%v) = %v, want allowed", args, err)
		}
	}
}
