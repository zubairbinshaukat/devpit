//go:build shots

package shots

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/home"
	installui "github.com/zubairbinshaukat/devpit/internal/ui/screens/install"
	updateui "github.com/zubairbinshaukat/devpit/internal/ui/screens/update"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// demoClock is the clock the install and update screens tell time by. It only
// moves when a fake command says so, which makes every "took 14s" on a
// screenshot the same on every run, however slow the machine drawing it is.
type demoClock struct {
	mu sync.Mutex
	t  time.Time
}

// newDemoClock starts at the moment the screenshots pretend to be taken.
func newDemoClock() *demoClock { return &demoClock{t: today} }

// now is the time.
func (c *demoClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// advance moves the clock on.
func (c *demoClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// moveTo presses Down until the cursor row of a list holds text, so a scene
// can say "the row for pnpm" instead of counting rows. The cursor row is the
// one that starts with the selection bar.
func (s *session) moveTo(text string) {
	s.t.Helper()
	for range 80 {
		for _, line := range strings.Split(s.text(), "\n") {
			if strings.HasPrefix(strings.TrimLeft(line, " "), "▌") && strings.Contains(line, text) {
				return
			}
		}
		s.key("down")
	}
	s.t.Fatalf("the cursor never reached %q; the frame is:\n%s", text, s.text())
}

// --- Install --------------------------------------------------------------

// installed are the programs the demo PC already has, by executable name.
var installed = map[string]bool{
	"node.exe": true, "git.exe": true, "Code.exe": true, "wt.exe": true, "pwsh.exe": true, "python.exe": true,
}

// installStep is one pretend install command. Which app it belongs to is read
// from the command line; a pass runs the progress lines and finishes, and the
// app named in hold stays at 48% until the session ends.
func installStep(clk *demoClock, hold string) installui.RunStepFunc {
	return func(ctx context.Context, argv []string, _ time.Duration, onLine func(string)) tools.StepResult {
		cmd := strings.Join(argv, " ")
		// Let the screen draw the previous app's result before the clock
		// moves again, so each row's time is the step's own.
		time.Sleep(3 * tick)
		onLine("Found package (1/1)")
		onLine("Downloading installer")
		onLine("  ██████████████░░░░░░░░░░░░░░  312 MB / 640 MB")
		if hold != "" && strings.Contains(cmd, hold) {
			<-ctx.Done()
			return tools.StepResult{ExitCode: -1}
		}
		onLine("  ██████████████████████████████  640 MB / 640 MB")
		onLine("Successfully installed")
		clk.advance(14 * time.Second)
		return tools.StepResult{OK: true, Elapsed: 14 * time.Second}
	}
}

// installFlow drives Install Developer Apps: it opens the section, ticks the
// named apps, then plays the remaining steps.
func installFlow(hold string, ticks []string, steps []string, want string) scene {
	return func(t *testing.T, e entry) *session {
		clk := newDemoClock()
		cfg := demoConfig()
		cfg.PreferredManager = "winget"
		s := newSession(t, e, cfg, screens{home.SectionInstall: func() uictx.Screen {
			return installui.New(
				installui.WithDetectFunc(func(context.Context) []tools.Tool {
					return []tools.Tool{{Name: "scoop", Found: true}, {Name: "winget", Found: true}}
				}),
				installui.WithLookPathFunc(func(exe string) (string, error) {
					if installed[exe] {
						return `C:\Tools\` + exe, nil
					}
					return "", context.DeadlineExceeded
				}),
				installui.WithIsElevatedFunc(func() (bool, error) { return true, nil }),
				installui.WithRunStepFunc(installStep(clk, hold)),
				installui.WithClock(clk.now),
			)
		}})
		s.clock = clk
		s.key(keyApps)
		s.waitFor(appsLead)
		s.key(keyInstall)
		s.waitFor("Manager:")
		for _, name := range ticks {
			s.moveTo(name)
			s.key("space")
		}
		s.run(steps...)
		s.waitFor(want)
		return s
	}
}

// --- Update ---------------------------------------------------------------

// wingetOutdated is what `winget upgrade` prints on the demo PC: six apps.
const wingetOutdated = `Name                Id                          Version   Available Source
--------------------------------------------------------------------------------
Docker Desktop      Docker.DockerDesktop        4.34.2    4.36.0    winget
Git                 Git.Git                     2.46.0    2.47.1    winget
Node.js LTS         OpenJS.NodeJS.LTS           22.9.0    22.11.0   winget
Visual Studio Code  Microsoft.VisualStudioCode  1.94.2    1.95.3    winget
GitHub CLI          GitHub.cli                  2.58.0    2.61.0    winget
PowerShell          Microsoft.PowerShell        7.4.5     7.4.6     winget
6 upgrades available.
`

// scoopOutdated is `scoop status`: three apps to update and one held back.
const scoopOutdated = `Name    Installed Version Latest Version Missing Dependencies Info
----    ----------------- -------------- -------------------- ----
ripgrep 14.1.0            14.1.1
fzf     0.55.0            0.56.3
lazygit 0.43.1            0.44.1
nvm     1.1.12            1.2.2                               Held package
`

// npmOutdated is `npm outdated -g --json`: three global packages.
const npmOutdated = `{"pnpm":{"current":"9.11.0","wanted":"9.14.2","latest":"9.14.2"},` +
	`"typescript":{"current":"5.6.2","wanted":"5.6.3","latest":"5.6.3"},` +
	`"npm-check-updates":{"current":"17.1.2","wanted":"17.1.9","latest":"17.1.9"}}`

// outcome says how one pretend upgrade ends.
type outcome int

const (
	// The zero outcome finishes cleanly.
	_ outcome = iota
	// outNeedsAdmin fails with winget's "needs administrator" code.
	outNeedsAdmin
	// outInUse fails with the "app is in use" code.
	outInUse
	// outCrash fails with a crash exit code.
	outCrash
	// outHold says a progress line and then waits until the run is stopped.
	outHold
	// outSilent says nothing and waits until the run is stopped.
	outSilent
)

// hresult turns an unsigned HRESULT into the signed exit code winget returns.
func hresult(code uint32) int { return int(int32(code)) }

// updateRunner answers every command the update screen runs. Checks return
// the demo tables. An upgrade ends as outcomes says for the app in its
// command line, defaulting to success, and advances the demo clock so
// durations are real-looking and fixed.
type updateRunner struct {
	clk      *demoClock
	outcomes map[string]outcome
}

// run is the RunStepFunc.
func (u *updateRunner) run(ctx context.Context, argv []string, _ time.Duration, onLine func(tools.Line)) tools.StepResult {
	cmd := strings.Join(argv, " ")
	switch cmd {
	case "winget upgrade --accept-source-agreements":
		return emit(onLine, wingetOutdated)
	case "scoop status":
		return emit(onLine, scoopOutdated)
	case "npm outdated -g --json":
		return emit(onLine, npmOutdated)
	case "scoop update":
		return emit(onLine, "Scoop was updated successfully!")
	}
	// Let the screen draw the previous app's result before the clock moves
	// again, so each row's time is the step's own.
	time.Sleep(3 * tick)
	app := appOf(cmd)
	took := durations[app]
	if took == 0 {
		took = 9 * time.Second
	}
	switch u.outcomes[app] {
	case outNeedsAdmin:
		u.clk.advance(3 * time.Second)
		return tools.StepResult{ExitCode: hresult(0x80073D28), Elapsed: 3 * time.Second}
	case outInUse:
		u.clk.advance(4 * time.Second)
		return tools.StepResult{ExitCode: hresult(0x80073D02), Elapsed: 4 * time.Second}
	case outCrash:
		u.clk.advance(6 * time.Second)
		return tools.StepResult{ExitCode: 3221226505, Elapsed: 6 * time.Second}
	case outHold:
		for _, l := range holdLines[app] {
			onLine(tools.Line{Text: l})
		}
		<-ctx.Done()
		return tools.StepResult{ExitCode: -1}
	case outSilent:
		<-ctx.Done()
		return tools.StepResult{ExitCode: -1}
	}
	onLine(tools.Line{Text: "Downloading"})
	onLine(tools.Line{Text: "Successfully installed"})
	u.clk.advance(took)
	return tools.StepResult{OK: true, Elapsed: took}
}

// durations is how long each demo upgrade takes, keyed by app id.
var durations = map[string]time.Duration{
	"npm-check-updates": 7 * time.Second, "pnpm": 5 * time.Second, "typescript": 6 * time.Second,
	"Docker.DockerDesktop": 48 * time.Second, "Microsoft.VisualStudioCode": 19 * time.Second,
	"GitHub.cli": 8 * time.Second, "Microsoft.PowerShell": 14 * time.Second, "Git.Git": 22 * time.Second,
	"OpenJS.NodeJS.LTS": 31 * time.Second, "ripgrep": 3 * time.Second, "fzf": 2 * time.Second, "lazygit": 3 * time.Second,
}

// holdLines is what an app that is still downloading has said so far.
var holdLines = map[string][]string{
	"Git.Git": {
		"Downloading https://github.com/git-for-windows/git/releases/download/v2.47.1.windows.1/Git-2.47.1-64-bit.exe",
		"  ██████████████████░░░░░░░░░░  38.4 MB / 62.1 MB",
	},
	"Docker.DockerDesktop": {
		"Downloading https://desktop.docker.com/win/main/amd64/Docker%20Desktop%20Installer.exe",
		"  ██████████████░░░░░░░░░░░░░░  312 MB / 640 MB",
	},
}

// appOf finds which demo app a command line is for: the winget id, the npm or
// scoop package name.
func appOf(cmd string) string {
	if strings.HasPrefix(cmd, "scoop cleanup") {
		return "scoop cleanup"
	}
	for _, app := range slices.Sorted(maps.Keys(durations)) {
		if strings.Contains(cmd, app) {
			return app
		}
	}
	return ""
}

// emit sends a canned table line by line and reports success.
func emit(onLine func(tools.Line), table string) tools.StepResult {
	for _, l := range strings.Split(strings.TrimRight(table, "\n"), "\n") {
		onLine(tools.Line{Text: l})
	}
	return tools.StepResult{OK: true}
}

// updateFlow drives Update Everything with the given per-app outcomes. It
// opens the section and plays steps; want is what must show before the frame.
// admin, when set, is how the admin prompt is answered.
func updateFlow(outcomes map[string]outcome, admin func() updateui.Option, steps []string, want string) scene {
	return func(t *testing.T, e entry) *session {
		clk := newDemoClock()
		run := &updateRunner{clk: clk, outcomes: outcomes}
		screen := func() uictx.Screen {
			opts := []updateui.Option{
				updateui.WithDetectFunc(func(context.Context) []tools.Tool {
					return []tools.Tool{{Name: "winget", Found: true}, {Name: "scoop", Found: true}, {Name: "npm", Found: true}}
				}),
				updateui.WithRunStepFunc(run.run),
				updateui.WithClock(clk.now),
			}
			if admin != nil {
				opts = append(opts, admin())
			}
			return updateui.New(opts...)
		}
		s := newSession(t, e, demoConfig(), screens{home.SectionUpdate: screen})
		s.clock = clk
		s.key(keyApps)
		s.waitFor(appsLead)
		s.key(keyUpdate)
		s.run(steps...)
		s.waitFor(want)
		return s
	}
}

// declineAdmin answers the admin prompt with "no".
func declineAdmin() updateui.Option { return launcher(nil, &elevate.DeclinedError{}) }

// holdAdmin answers the admin prompt with "yes" and then runs the retried
// commands forever, so a frame can show "retrying as administrator".
func holdAdmin() updateui.Option { return launcher(&fakeWorker{hold: true}, nil) }
