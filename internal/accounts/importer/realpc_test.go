package importer

import (
	"context"
	"os"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// TestRealPCReadOnly prints what Detect and PlanImport find on this PC. It
// changes nothing and runs no tool. Opt in with DEVPIT_REAL_IMPORT=1.
func TestRealPCReadOnly(t *testing.T) {
	if os.Getenv("DEVPIT_REAL_IMPORT") == "" {
		t.Skip("set DEVPIT_REAL_IMPORT=1")
	}
	home, _ := os.UserHomeDir()
	f, err := Detect(context.Background(), Deps{Home: home}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(f.Summary())
	for _, p := range f.Problems {
		t.Log("problem: " + p)
	}
	if f.ClaudeAcc != nil {
		for _, a := range f.ClaudeAcc.Accounts {
			t.Logf("account %s dir=%s email-known=%v org-known=%v signed-in-file=%v", a.Name, a.Dir, a.Email != "", a.Org != "", a.SignedIn)
		}
		for _, pl := range f.ClaudeAcc.ProfileLines {
			t.Logf("profile line: %s:%d (header %d)", pl.File, pl.Line, pl.HeaderLine)
		}
	}
	p, err := PlanImport(accounts.NewStore(), f, Options{ClaudeAcc: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range p.Lines() {
		t.Log(l)
	}
	for _, s := range RemovalGuide(f) {
		t.Log("STEP: " + s.Title)
	}
}
