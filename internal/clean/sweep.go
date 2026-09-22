package clean

import (
	"context"
	"io/fs"
	"path/filepath"
	"time"
)

// tombstoneCandidate is a directory whose name matches Devpit's tombstone
// pattern. err is nil only when the marker inside it proves Devpit made it;
// otherwise err says why the directory is a stranger's and is reported as a
// skip, never acted on.
type tombstoneCandidate struct {
	path string
	err  error
}

// findTombstoneCandidates walks each root and returns every directory named
// like a tombstone, each one already judged by [verifyTombstone].
//
// Roots that cannot be walked are skipped rather than reported; a sweep is
// housekeeping and must never be the reason a run fails. A candidate is never
// descended into: if it is Devpit's, everything below it is already condemned,
// and if it is not, nothing below it is Devpit's business either.
func findTombstoneCandidates(ctx context.Context, roots []string) []tombstoneCandidate {
	if ctx == nil {
		ctx = context.Background()
	}
	var found []tombstoneCandidate
	seen := make(map[string]struct{})
	for _, root := range roots {
		abs, err := normalize(root)
		if err != nil {
			continue
		}
		_ = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return filepath.SkipAll
			}
			if err != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			// WalkDir does not follow links, and a link is never a tombstone.
			if d.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
				return nil
			}
			if !IsTombstone(d.Name()) {
				return nil
			}
			if _, dup := seen[p]; dup {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			seen[p] = struct{}{}
			if !d.IsDir() {
				// A file cannot hold a marker, so nothing can ever prove
				// Devpit made it. Devpit only ever tombstones directories.
				found = append(found, tombstoneCandidate{
					path: p,
					err:  wrapNotATombstone(p, "it is not a directory"),
				})
				return nil
			}
			found = append(found, tombstoneCandidate{path: p, err: verifyTombstone(p)})
			return filepath.SkipDir
		})
	}
	return found
}

// FindTombstones walks each root and returns the tombstones it finds: the
// directories that match the name pattern *and* carry a [TombstoneMarker]
// naming the sibling path they were renamed from. A folder a user happens to
// have called `archive.devpit-abc12345` matches the pattern and is not a
// tombstone; it is never returned and never touched.
func FindTombstones(ctx context.Context, roots []string) []string {
	var found []string
	for _, c := range findTombstoneCandidates(ctx, roots) {
		if c.err == nil {
			found = append(found, c.path)
		}
	}
	return found
}

// Sweep finishes the tombstones left under roots by an interrupted or locked
// run. This is what makes interrupting a delete safe: the rename already
// happened, so there is never a half-emptied folder that looks like a working
// one, only a `.devpit-<random>` directory waiting for this.
//
// Nothing is removed on the strength of its name. A directory is swept only
// when it holds the marker Devpit wrote the instant it renamed it, and only
// after [preflightTombstone] has passed on both that directory and the path
// the marker says it came from: the never-touch list may have grown since, and
// a tombstone that is now a reparse point is not one Devpit will follow.
// Everything else that matched the pattern is reported in Report.Skipped and
// left exactly as it was.
//
// A tombstone whose marker could not be written when it was created is one of
// the things a sweep leaves alone. The run that made it said so in its own
// report; there is no way, later, to tell such a folder from a stranger's.
//
// Report.Freed is zero: the space was reported freed when the rename happened,
// and counting it twice would be a lie.
func Sweep(ctx context.Context, roots []string, opts Options, progress func(Progress)) Report {
	started := time.Now()
	report := Report{}
	if ctx == nil {
		ctx = context.Background()
	}

	tombs := findTombstoneCandidates(ctx, roots)
	for i, tomb := range tombs {
		it := Item{Path: tomb.path, Tier: TierSafe}
		if err := ctx.Err(); err != nil {
			report.Skipped = append(report.Skipped, Result{Item: it, Err: err, Reason: reasonFor(it, err)})
			continue
		}
		if tomb.err != nil {
			report.Skipped = append(report.Skipped, Result{Item: it, Err: tomb.err, Reason: reasonFor(it, tomb.err)})
			continue
		}
		if err := preflightTombstone(tomb.path, "", opts); err != nil {
			report.Skipped = append(report.Skipped, Result{Item: it, Err: err, Reason: reasonFor(it, err)})
			continue
		}
		report.emit(progress, Progress{Index: i, Total: len(tombs), Path: tomb.path})
		err := removeTree(tomb.path, opts.workers(), func(done, total int, _ string) {
			report.emit(progress, Progress{
				Index: i, Total: len(tombs), Path: tomb.path,
				ChildDone: done, ChildTotal: total,
			})
		})
		if err != nil {
			report.Skipped = append(report.Skipped, lockedOrPlain(it, tomb.path, err))
			continue
		}
		report.Deleted = append(report.Deleted, Result{Item: it, Reason: reasonFor(it, nil)})
	}

	report.Elapsed = time.Since(started)
	return report
}

// Resume is Sweep under the name `devpit clean --resume` and the startup sweep
// use. It exists so callers read as what they mean.
func Resume(ctx context.Context, roots []string, opts Options, progress func(Progress)) Report {
	return Sweep(ctx, roots, opts, progress)
}
