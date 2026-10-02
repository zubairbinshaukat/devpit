package accounts

import (
	"strings"
	"testing"
)

func previewStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	must(t, s.AddAccount(Account{Tool: ToolClaude, Name: "work", Email: "zubair@work.com", Dir: `C:\a\work`}))
	must(t, s.AddAccount(Account{Tool: ToolClaude, Name: "oss", Email: "zubair@proton.me", Dir: `C:\a\oss`}))
	return s
}

var defaults = map[Tool]string{ToolClaude: "zubair@gmail.com"}

func TestPreviewFolderWording(t *testing.T) {
	s := previewStore(t)
	p, err := BuildPreview(PreviewInput{Store: s, Change: Change{Tool: ToolClaude, Account: "work", Scope: FolderScope(`c:\work\`)}, Defaults: defaults})
	must(t, err)
	want := []string{
		`In C:\work and every folder inside it, Claude Code will use work (zubair@work.com).`,
		`Everywhere else stays default (zubair@gmail.com).`,
	}
	if strings.Join(p.Sentences, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sentences:\n%s", strings.Join(p.Sentences, "\n"))
	}
	if p.Summary != `Claude Code uses work in C:\work` || p.NoChange || p.DriveRoot {
		t.Fatalf("p = %+v", p)
	}
	if len(p.Edits) != 1 || p.Edits[0].Action != "adds" ||
		strings.Join(p.Edits[0].Lines, "|") != `[[rule]]|folder = 'C:\work'|claude = "work"` {
		t.Fatalf("edits = %+v", p.Edits)
	}
}

func TestPreviewEverywhereListsRulesThatStillWin(t *testing.T) {
	s := previewStore(t)
	must(t, s.SetRule(`C:\Projects\quiz-slayer`, ToolClaude, "default"))
	must(t, s.SetRule(`D:\oss`, ToolClaude, "oss"))
	must(t, s.SetRule(`D:\gh`, ToolGitHub, "default")) // another tool: not listed
	p, err := BuildPreview(PreviewInput{Store: s, Change: Change{Tool: ToolClaude, Account: "wo", Scope: EverywhereScope()}, Defaults: defaults})
	must(t, err)
	got := strings.Join(p.Lines(), "\n")
	want := strings.Join([]string{
		"Everywhere, Claude Code will use work (zubair@work.com).",
		"2 folders keep their own rule and will not change:",
		`  C:\Projects\quiz-slayer   default (zubair@gmail.com)`,
		`  D:\oss                    oss (zubair@proton.me)`,
		"",
		"Devpit will write:",
		"  accounts.toml (changes)",
		"    [everywhere]",
		`    claude = "work"`,
	}, "\n")
	if got != want {
		t.Fatalf("preview:\n%s\n--- want:\n%s", got, want)
	}
}

func TestPreviewNestedRules(t *testing.T) {
	s := previewStore(t)
	must(t, s.SetRule(`C:\Work`, ToolClaude, "work"))
	must(t, s.SetRule(`C:\Work\oss\lib`, ToolClaude, "oss"))
	p, err := BuildPreview(PreviewInput{Store: s, Change: Change{Tool: ToolClaude, Account: "oss", Scope: FolderScope(`C:\Work\oss`)}, Defaults: defaults})
	must(t, err)
	want := []string{
		`In C:\Work\oss and every folder inside it, Claude Code will use oss (zubair@proton.me).`,
		`The rest of C:\Work stays work (zubair@work.com).`,
		`Everywhere else stays default (zubair@gmail.com).`,
		`1 folder inside it keeps its own rule and will not change:`,
		`  C:\Work\oss\lib   oss (zubair@proton.me)`,
	}
	got := p.Lines()[:len(want)]
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines:\n%s", strings.Join(p.Lines(), "\n"))
	}
}

func TestPreviewRemoveNoChangeOnceAndDriveRoot(t *testing.T) {
	s := previewStore(t)
	must(t, s.SetRule(`C:\Work`, ToolClaude, "work"))
	must(t, s.SetRule(`C:\Work\api`, ToolClaude, "oss"))

	p, err := BuildPreview(PreviewInput{Store: s, Change: Change{Tool: ToolClaude, Remove: true, Scope: FolderScope(`C:\Work\api`)}, Defaults: defaults})
	must(t, err)
	if p.Sentences[0] != `C:\Work\api will no longer have its own Claude Code rule. It and every folder inside it will use work (zubair@work.com), from the rule on C:\Work.` {
		t.Fatalf("remove: %q", p.Sentences[0])
	}
	if p.Edits[0].Action != "removes" {
		t.Fatalf("edit = %+v", p.Edits[0])
	}

	p, _ = BuildPreview(PreviewInput{Store: s, Change: Change{Tool: ToolClaude, Account: "WORK", Scope: FolderScope(`c:\work`)}})
	if !p.NoChange || p.Sentences[0] != `Claude Code already uses work (zubair@work.com) in C:\work. Nothing will change.` {
		t.Fatalf("no-op: %+v", p)
	}

	p, _ = BuildPreview(PreviewInput{Store: s, Change: Change{Tool: ToolClaude, Account: "default", Scope: EverywhereScope()}, Defaults: defaults})
	if !p.NoChange || p.Sentences[0] != "Claude Code already uses default (zubair@gmail.com) everywhere. Nothing will change." {
		t.Fatalf("everywhere no-op: %+v", p.Sentences)
	}

	p, _ = BuildPreview(PreviewInput{Store: s, Change: Change{Tool: ToolClaude, Account: "oss", Scope: OnceScope()}})
	if p.Sentences[0] != "Just this once, Claude Code will use oss (zubair@proton.me). Nothing is saved." || len(p.Edits) != 0 {
		t.Fatalf("once: %+v", p)
	}

	p, _ = BuildPreview(PreviewInput{Store: s, Change: Change{Tool: ToolClaude, Account: "oss", Scope: FolderScope(`D:\`)}, Home: `C:\Users\z`})
	if !p.DriveRoot || !strings.Contains(p.SecondConfirm, `every folder on D:\`) {
		t.Fatalf("drive root: %+v", p)
	}
	p, _ = BuildPreview(PreviewInput{Store: s, Change: Change{Tool: ToolClaude, Account: "oss", Scope: FolderScope(`c:\users\Z\`)}, Home: `C:\Users\z`})
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "home folder") {
		t.Fatalf("home: %+v", p.Warnings)
	}

	if _, err := BuildPreview(PreviewInput{Store: s, Change: Change{Tool: ToolClaude, Account: "nobody", Scope: EverywhereScope()}}); err == nil {
		t.Fatal("an unknown account must be refused")
	}
	if _, err := BuildPreview(PreviewInput{Store: s, Change: Change{Tool: ToolClaude, Account: "work", Scope: FolderScope("relative")}}); err == nil {
		t.Fatal("a relative folder must be refused")
	}
}

func TestPreviewCustomWording(t *testing.T) {
	s := NewStore()
	must(t, s.AddAccount(Account{Tool: ToolGit, Name: "work", Label: "Zubair", Email: "zubair@work.com"}))
	p, err := BuildPreview(PreviewInput{
		Store: s, Change: Change{Tool: ToolGit, Account: "work", Scope: FolderScope(`C:\Work`)},
		Display: func(a Account) string {
			if a.IsDefault() {
				return "Zubair <zubair@gmail.com>"
			}
			return a.Label + " <" + a.Email + ">"
		},
		Uses:  func(tool, acct string) string { return tool + " will commit as " + acct },
		Edits: []FileEdit{{Path: `~/.devpit/git/rules.gitconfig`, Action: "changes", Lines: []string{`[includeIf "gitdir/i:C:/Work/"] path = work.gitconfig`}}},
	})
	must(t, err)
	got := strings.Join(p.Sentences, "\n")
	if got != "In C:\\Work and every folder inside it, Git will commit as Zubair <zubair@work.com>.\nEverywhere else stays Zubair <zubair@gmail.com>." {
		t.Fatalf("got:\n%s", got)
	}
	if len(p.Edits) != 2 || p.Edits[1].Path != `~/.devpit/git/rules.gitconfig` {
		t.Fatalf("edits = %+v", p.Edits)
	}
}

func TestApplyToDoesNotTouchTheOriginal(t *testing.T) {
	s := previewStore(t)
	before := StoreHash(s)
	after, err := Change{Tool: ToolClaude, Account: "work", Scope: FolderScope(`C:\Work`)}.ApplyTo(s)
	must(t, err)
	if StoreHash(s) != before || StoreHash(after) == before {
		t.Fatal("ApplyTo must work on a copy")
	}
}
