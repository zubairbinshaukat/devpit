//go:build shots

package shots

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	accountsui "github.com/zubairbinshaukat/devpit/internal/ui/screens/accounts"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/accounts/demo"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/home"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The Accounts screens run over the in-memory demo engine the screens' own
// golden tests use: the real store, resolver and preview sentences, with
// every tool, sign-in and file faked. Nothing a scene does can run a tool,
// open a browser or touch a real account, ~/.claude, ~/.gitconfig or PATH.
// Every name, email and path is the demo's own made-up machine.

// dirInfo is a folder that exists, for the screens' Stat.
type dirInfo struct{ os.FileInfo }

func (dirInfo) IsDir() bool { return true }

// accountsOptions are the screen options over svc: the demo folder, no
// spinner timers, nothing handed to a real terminal, and a clipboard that
// keeps nothing.
func accountsOptions(svc *demo.Service) accountsui.Options {
	return accountsui.Options{
		Folder: demo.Folder,
		Open:   func() (accountsui.Service, error) { return svc, nil },
		Exec: func(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd {
			return func() tea.Msg { return fn(nil) }
		},
		Copy: func(string) tea.Cmd { return nil },
		Tick: func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil },
		Stat: func(p string) (os.FileInfo, error) {
			if svc.Gone[strings.ToLower(p)] {
				return nil, os.ErrNotExist
			}
			return dirInfo{}, nil
		},
		Getwd:   func() (string, error) { return demo.Folder, nil },
		Browse:  func(string) (string, error) { return `D:\elsewhere`, nil },
		RunOnce: func(service.OnceCommand) (int, error) { return 0, nil },
	}
}

// accountsAt opens Accounts from the home menu over a demo machine (shaped
// by setup, when given), plays the steps and waits for want. A step written
// "to:text" moves the cursor down to the row that shows text.
func accountsAt(setup func(*demo.Service), steps []string, want ...string) scene {
	return func(t *testing.T, e entry) *session {
		svc := demo.New()
		if setup != nil {
			setup(svc)
		}
		s := newSession(t, e, demoConfig(), screens{
			home.SectionAccounts: func() uictx.Screen { return accountsui.NewWith(accountsOptions(svc)) },
		})
		s.key(keyAccounts)
		s.waitFor("set by this project")
		for _, st := range steps {
			if text, ok := strings.CutPrefix(st, "to:"); ok {
				s.moveTo(text)
				continue
			}
			s.run(st)
		}
		s.waitFor(want...)
		return s
	}
}

// withImport is a PC where claude-acc is installed, so Accounts opens on
// "Found accounts already on this PC".
func withImport(svc *demo.Service) { svc.Found = demo.SampleFound() }

// accountsImport opens Accounts on a PC with claude-acc: the page shows the
// import card first, so it does not wait for the table.
func accountsImport(want string) scene {
	return func(t *testing.T, e entry) *session {
		svc := demo.New()
		withImport(svc)
		s := newSession(t, e, demoConfig(), screens{
			home.SectionAccounts: func() uictx.Screen { return accountsui.NewWith(accountsOptions(svc)) },
		})
		s.key(keyAccounts)
		s.waitFor(want)
		return s
	}
}

// toolRows are the Accounts table rows a scene opens: Claude Code is the
// first, Git the second.
const (
	rowClaude = 0
	rowGit    = 1
)

// claudeSetupSteps move from the top of the Claude Code page to "Bring your
// Claude Code setup over…" and open it.
var claudeSetupSteps = []string{"to:Bring your Claude Code setup over", "enter"}

// withConflicts gives the other account two skills of its own with the same
// names as the main account's, so the setup asks what to do with them.
func withConflicts(svc *demo.Service) { svc.Inventory.Items[0].Conflicts = 2 }

// openRow is the steps that move to a table row and open its tool page.
func openRow(row int, more ...string) []string {
	steps := make([]string, 0, row+1+len(more))
	for range row {
		steps = append(steps, "down")
	}
	steps = append(steps, "enter")
	return append(steps, more...)
}
