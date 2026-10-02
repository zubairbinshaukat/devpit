package accounts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	plantedGH  = "ghp_Ab1Ab1Ab1Ab1Ab1Ab1Ab1Ab1Ab1Ab1Ab1Ab1"
	plantedRaw = "Zq9x7Yx7Yx7Yx7Yx7Yx7Yx7Yx7Yx7Yx7Yx7Yx7Y"
	longName   = "a1b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"
)

// Paths are judged by the known-prefix rule, free text by the strict one,
// and a planted token is caught by whichever rule applies.
func TestPathRulesAndTheStrictRule(t *testing.T) {
	longPath := `C:\Users\me\AppData\Local\Temp\` + longName + `\Work`
	if !LooksSecret(longName) || PathLooksSecret(longPath) || ContentLooksSecret("path = \""+filepath.ToSlash(longPath)+"\"\n") {
		t.Fatal("a long folder name must be a secret to the strict rule only")
	}
	if !PathLooksSecret(`C:\x\`+plantedGH) || !ContentLooksSecret(`path = C:/x/`+plantedGH) {
		t.Fatal("a known token prefix inside a path must still count")
	}
	for _, s := range []string{
		"token = " + plantedRaw,
		"url = https://" + plantedRaw + "@github.com/x.git",
		`[credential] helper = store --file C:/x token=` + plantedRaw,
		plantedRaw,
	} {
		if !ContentLooksSecret(s) {
			t.Errorf("ContentLooksSecret(%q) = false", s)
		}
	}
	// Free text keeps the strict rule.
	if got := Scrub("in " + longPath); !strings.Contains(got, Hidden) {
		t.Fatalf("Scrub = %q", got)
	}
	if got := ScrubKeepingPaths("Claude Code uses work in " + longPath); strings.Contains(got, Hidden) {
		t.Fatalf("ScrubKeepingPaths = %q", got)
	}
	if got := ScrubKeepingPaths("in " + longPath + " and " + plantedRaw); !strings.Contains(got, longName) || strings.Contains(got, plantedRaw) {
		t.Fatalf("ScrubKeepingPaths = %q", got)
	}
	if p, ok := IsAbsPathArg("--global-config=" + longPath); !ok || p != longPath {
		t.Fatal("IsAbsPathArg flag form")
	}
	if _, ok := IsAbsPathArg("relative\\path"); ok {
		t.Fatal("IsAbsPathArg relative")
	}
}

// The journal takes long folder names in targets, summaries and Data, and a
// planted token never reaches it.
func TestJournalTakesLongFoldersAndRefusesTokens(t *testing.T) {
	e := newTestEngine(t)
	dir := filepath.Join(t.TempDir(), longName)
	must(t, os.MkdirAll(dir, 0o700))
	target := filepath.Join(dir, "rules.gitconfig")

	txn, err := e.Begin("Git uses work in "+dir, addWork)
	must(t, err)
	must(t, txn.WriteFile(target, "rules in "+dir, []byte("[includeIf \"gitdir/i:"+filepath.ToSlash(dir)+"/\"]\n    path = \"work.gitconfig\"\n")))
	must(t, txn.Do(Effect{Kind: EffectFile, Target: target + ".x", Data: map[string]string{"dir": dir, "hash": strings.Repeat("ab12", 16)}},
		func() (Effect, error) { return Effect{Kind: EffectFile, Target: target + ".x", AfterHash: ""}, nil }))
	ent, err := txn.Commit()
	must(t, err)
	if !strings.Contains(ent.Summary, longName) || !strings.Contains(ent.Effects[0].Summary, longName) {
		t.Fatalf("summary = %q / %q", ent.Summary, ent.Effects[0].Summary)
	}

	txn, err = e.Begin("planted", nil)
	must(t, err)
	defer func() { _ = txn.Rollback() }()
	for _, data := range []string{"token = " + plantedRaw + "\n", "path = C:/x/" + plantedGH + "\n", plantedRaw} {
		if werr := txn.WriteFile(filepath.Join(dir, "x.txt"), "x", []byte(data)); werr == nil {
			t.Errorf("WriteFile took %q", data)
		}
	}
	for _, d := range []map[string]string{{"dir": `C:\x\` + plantedGH}, {"token": "x"}, {"api_key": "x"}} {
		derr := txn.Do(Effect{Kind: EffectFile, Target: target, Data: d}, func() (Effect, error) { return Effect{}, nil })
		if derr == nil {
			t.Errorf("Data %v was recorded", d)
		}
	}
	journal, err := os.ReadFile(e.Paths.Journal)
	must(t, err)
	for _, tok := range []string{plantedGH, plantedRaw} {
		if strings.Contains(string(journal), tok) {
			t.Fatalf("the journal holds %s", tok)
		}
	}
}
