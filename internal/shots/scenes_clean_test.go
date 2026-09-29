//go:build shots

package shots

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/config"

	cleanengine "github.com/zubairbinshaukat/devpit/internal/clean"
	"github.com/zubairbinshaukat/devpit/internal/scan"
	"github.com/zubairbinshaukat/devpit/internal/scan/rules"
	cleanui "github.com/zubairbinshaukat/devpit/internal/ui/screens/clean"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/home"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// today is the moment the screenshots pretend to be taken. Every "3 mo ago"
// on a screen is measured from it, so the frames never change with the date.
var today = time.Date(2026, 9, 29, 9, 41, 0, 0, time.UTC)

// stepClock returns a clock that moves a second on every read, so the clean
// screen's 5 ms key debounce never swallows one of a scene's key presses.
func stepClock() func() time.Time {
	now := today
	return func() time.Time {
		now = now.Add(time.Second)
		return now
	}
}

// ruleByName looks a scanner rule up by name, so a demo item carries the
// real tier, kind, markers and restore hint of the rule that would find it.
// Only the rule table is read: nothing here walks a disk.
var ruleByName = func() map[string]scan.Rule {
	out := map[string]scan.Rule{}
	for _, r := range rules.All() {
		out[r.Name] = r
	}
	for _, r := range rules.LargeFiles() {
		out[r.Name] = r
	}
	return out
}()

// found describes one demo find.
type found struct {
	rule string
	// dir is the folder name on disk, such as ".next".
	dir string
	// repo and project are the git repository and the project folder.
	repo, project string
	size          uint64
	// ageDays is how long ago the project was last touched.
	ageDays    int
	unverified bool
}

// mb and gb are byte counts.
const (
	mb = 1 << 20
	gb = 1 << 30
)

// item turns a description into a scan.Item the way the scanner would.
func (f found) item() scan.Item {
	r, ok := ruleByName[f.rule]
	if !ok {
		panic("shots: unknown scan rule " + f.rule)
	}
	project := f.project
	if project == "" {
		project = f.repo
	}
	return scan.Item{
		Path:        project + `\` + f.dir,
		Name:        f.dir,
		Project:     project,
		Repo:        f.repo,
		Size:        f.size,
		Tier:        r.Tier,
		Kind:        r.Kind,
		Rule:        r.Name,
		RestoreHint: r.RestoreHint,
		Markers:     r.Markers,
		LastUsed:    today.AddDate(0, 0, -f.ageDays),
		Active:      f.ageDays <= 7,
		Unverified:  f.unverified,
	}
}

// items converts a list.
func items(fs []found) []scan.Item {
	out := make([]scan.Item, len(fs))
	for i, f := range fs {
		out[i] = f.item()
	}
	return out
}

// Demo projects. The monorepo shows how projects nest under their repository;
// its files were touched two days ago, so it is marked active and not ticked.
const (
	monorepo = demoProjects + `\acme-platform`
	shop     = demoProjects + `\shopfront`
	mobile   = demoProjects + `\mobile-app`
	notes    = demoProjects + `\notes-cli`
	hackday  = demoProjects + `\archive\hackathon-2024`
)

// projectJunk is what a Project Junk scan of the demo projects folder finds.
func projectJunk() []scan.Item {
	return items([]found{
		{rule: "node_modules", dir: "node_modules", repo: monorepo, size: 1_460 * mb, ageDays: 2},
		{rule: "next", dir: ".next", repo: monorepo, project: monorepo + `\apps\web`, size: 412 * mb, ageDays: 2},
		{rule: "node_modules", dir: "node_modules", repo: monorepo, project: monorepo + `\apps\web`, size: 236 * mb, ageDays: 2},
		{rule: "dist", dir: "dist", repo: monorepo, project: monorepo + `\apps\admin`, size: 38 * mb, ageDays: 2},
		{rule: "dist", dir: "dist", repo: monorepo, project: monorepo + `\packages\ui`, size: 12 * mb, ageDays: 2},
		{rule: "node_modules", dir: "node_modules", repo: shop, size: 987 * mb, ageDays: 94},
		{rule: "next", dir: ".next", repo: shop, size: 301 * mb, ageDays: 94},
		{rule: "dist", dir: "dist", repo: shop, size: 54 * mb, ageDays: 94},
		{rule: "node_modules", dir: "node_modules", repo: mobile, size: 1_130 * mb, ageDays: 41},
		{rule: "expo cache", dir: ".expo", repo: mobile, size: 96 * mb, ageDays: 41},
		{rule: "cargo target", dir: "target", repo: notes, size: 2_350 * mb, ageDays: 63},
		{rule: "node_modules", dir: "node_modules", repo: hackday, size: 610 * mb, ageDays: 430, unverified: true},
	})
}

// nodeModulesOnly is the node_modules folders of the demo projects, for the
// scene that shows just those.
func nodeModulesOnly() []scan.Item {
	var out []scan.Item
	for _, it := range projectJunk() {
		if it.Name == "node_modules" {
			out = append(out, it)
		}
	}
	return out
}

// appData is where the demo user's package caches live.
const appData = `C:\Users\` + demoUser + `\AppData\Local`

// fullScan is what a Full Scan finds: a few projects, package caches, and one
// large file, which is Careful.
func fullScan() []scan.Item {
	all := items([]found{
		{rule: "node_modules", dir: "node_modules", repo: shop, size: 987 * mb, ageDays: 94},
		{rule: "next", dir: ".next", repo: shop, size: 301 * mb, ageDays: 94},
		{rule: "node_modules", dir: "node_modules", repo: mobile, size: 1_130 * mb, ageDays: 41},
		{rule: "cargo target", dir: "target", repo: notes, size: 2_350 * mb, ageDays: 63},
		{rule: "npm cache", dir: "_cacache", project: appData + `\npm-cache`, size: 1_840 * mb, ageDays: 3},
		{rule: "pnpm store", dir: "store", project: appData + `\pnpm`, size: 6_200 * mb, ageDays: 1},
		{rule: "nuget packages", dir: "packages", project: `C:\Users\` + demoUser + `\.nuget`, size: 2_420 * mb, ageDays: 20},
		{rule: "gradle cache", dir: "caches", project: `C:\Users\` + demoUser + `\.gradle`, size: 3_180 * mb, ageDays: 12},
		{rule: "large installer or disk image", dir: "ubuntu-24.04-desktop-amd64.iso", project: demoProjects + `\downloads`, size: 6_040 * mb, ageDays: 150},
	})
	// Package caches and large files are single locations, not project
	// folders: they have no repository and their own name in the path.
	for i := range all {
		if all[i].Kind == scan.KindPackageCache {
			all[i].Path = all[i].Project + `\` + all[i].Name
		}
		if all[i].Kind == scan.KindLargeFile {
			all[i].Path = all[i].Project + `\` + all[i].Name
		}
	}
	return all
}

// fakeScan is a scanner that "finds" items and then, when holdAfter is above
// zero, stops after that many and waits as a scan of a big disk would, until
// it is cancelled. Zero means it finishes.
func fakeScan(all []scan.Item, holdAfter int) cleanui.ScanFunc {
	return func(ctx context.Context, _ scan.Options, out chan<- scan.Item, progress func(scan.Stats)) (scan.Stats, error) {
		var st scan.Stats
		for i, it := range all {
			if holdAfter > 0 && i == holdAfter {
				<-ctx.Done()
				return st, fmt.Errorf("scan stopped: %w", ctx.Err())
			}
			st.DirsChecked += 1_412
			st.Found++
			st.Bytes += it.Size
			st.Elapsed += 1_300 * time.Millisecond
			progress(st)
			select {
			case out <- it:
			case <-ctx.Done():
				return st, fmt.Errorf("scan stopped: %w", ctx.Err())
			}
		}
		return st, nil
	}
}

// fakeClean is a delete engine that deletes nothing. It reports progress for
// each item; with hold set it stops after the given item and waits, as a
// delete of a big node_modules does. locked names items to report as open in
// another program, with the program that holds them.
func fakeClean(hold int, locked map[string]string) cleanui.CleanFunc {
	return func(ctx context.Context, its []cleanengine.Item, _ cleanengine.Options, progress func(cleanengine.Progress)) cleanengine.Report {
		var rep cleanengine.Report
		for i, it := range its {
			progress(cleanengine.Progress{Index: i, Total: len(its), Path: it.Path, ChildDone: 3, ChildTotal: 8, Freed: rep.Freed})
			if hold > 0 && i == hold {
				<-ctx.Done()
				return rep
			}
			if by, ok := locked[it.Path]; ok {
				rep.Skipped = append(rep.Skipped, cleanengine.Result{
					Item: it,
					Err:  &cleanengine.LockedError{Path: it.Path, Holders: []cleanengine.Holder{{PID: 18244, Name: by}}, Err: errors.New("the process cannot access the file")},
					Reason: fmt.Sprintf("Couldn't delete %s — it's open in %s. Close it and press R to retry.",
						lastTwo(it.Path), by),
					Holders: []cleanengine.Holder{{PID: 18244, Name: by}},
				})
				continue
			}
			rep.Deleted = append(rep.Deleted, cleanengine.Result{Item: it})
			rep.Freed += it.Size
		}
		rep.Elapsed = 41 * time.Second
		return rep
	}
}

// lastTwo names a Windows path by its last two parts, as the engine does.
func lastTwo(p string) string {
	parts := splitWin(p)
	if len(parts) < 2 {
		return p
	}
	return parts[len(parts)-2] + `\` + parts[len(parts)-1]
}

// splitWin splits a Windows path on backslashes, whatever the host OS.
func splitWin(p string) []string {
	var parts []string
	cur := ""
	for _, r := range p {
		if r == '\\' {
			parts = append(parts, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(parts, cur)
}

// cleanScreen is the Free Up Disk Space screen over demo engines. Nothing is
// scanned, cached, deleted or reported.
func cleanScreen(found []scan.Item, holdScan int, del cleanui.CleanFunc) func() uictx.Screen {
	return func() uictx.Screen {
		return cleanui.New().WithClock(stepClock()).WithEngines(cleanui.Engines{
			Scan:  fakeScan(found, holdScan),
			Clean: del,
			Retry: func(context.Context, cleanengine.Item, cleanengine.Options, func(cleanengine.Progress)) cleanengine.Result {
				return cleanengine.Result{}
			},
			Sweep: func(context.Context, []string, cleanengine.Options, func(cleanengine.Progress)) cleanengine.Report {
				return cleanengine.Report{}
			},
			LoadCache: func(string) (*scan.Cache, error) { return nil, errors.New("no cache") },
			SaveCache: func(*scan.Cache) error { return nil },
			Verify:    func(scan.Item) error { return nil },
			Report:    func(context.Context, config.Config, cleanengine.Report, map[string]string) error { return nil },
			Getenv:    func(string) string { return "1" },
		})
	}
}

// cleanFlow is one way through the cleaner. The section is opened first;
// steps then play as in session.run, and want is what must be on screen
// before the frame is taken. cfgFn may change the configuration first.
type cleanFlow struct {
	found []scan.Item
	hold  int
	del   cleanui.CleanFunc
	cfgFn func(*config.Config)
	steps []string
	want  []string
}

// scene builds the scene of a flow.
func (f cleanFlow) scene() scene {
	return func(t *testing.T, e entry) *session {
		cfg := demoConfig()
		if f.cfgFn != nil {
			f.cfgFn(&cfg)
		}
		del := f.del
		if del == nil {
			del = fakeClean(0, nil)
		}
		s := newSession(t, e, cfg, screens{home.SectionClean: cleanScreen(f.found, f.hold, del)})
		s.key(keyClean)
		s.waitFor("Project Junk")
		s.run(f.steps...)
		s.waitFor(f.want...)
		return s
	}
}
