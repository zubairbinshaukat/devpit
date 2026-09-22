// Package scan finds reclaimable space on a developer machine.
//
// The engine is deliberately free of any user-interface dependency: it takes
// an [Options] value, streams [Item] values on a channel and reports counters
// through a callback. Nothing in this package imports Bubble Tea, lipgloss or
// anything under internal/ui, so the same code backs the TUI, the headless
// `devpit clean --json` command and the tests.
//
// The scanning contract, in one paragraph: walk the requested roots in
// parallel, never follow a reparse point, never call os.Stat per file, prune
// the moment a directory matches a rule whose marker file sits beside it, and
// size those pruned directories in a separate bounded pool. Everything that
// could not be read is counted rather than swallowed, and a cancelled scan
// keeps whatever it already found.
package scan

import (
	"strings"
	"time"
)

// Tier is how much care an item needs before it is deleted. It maps directly
// onto the three-way risk model the UI shows and the delete engine honours:
// Safe items are permanently deleted, Review items go to the Recycle Bin, and
// Careful items go to the Recycle Bin only after the user types a word.
type Tier int

// The three risk tiers, ordered from least to most dangerous.
const (
	// TierSafe is regenerable from a lockfile or a build command.
	TierSafe Tier = iota
	// TierReview is regenerable but costs time or bandwidth to rebuild.
	TierReview
	// TierCareful may hold something the user cannot get back.
	TierCareful
)

// String returns the tier's display word: "Safe", "Review" or "Careful".
func (t Tier) String() string {
	switch t {
	case TierSafe:
		return "Safe"
	case TierReview:
		return "Review"
	case TierCareful:
		return "Careful"
	default:
		return "Unknown"
	}
}

// Kind groups items into the categories the Full Scan screen lists. It exists
// so the results table can offer per-category toggles without parsing rule
// names.
type Kind int

// The scan categories.
const (
	// KindProjectJunk is build output and dependency folders inside a project.
	KindProjectJunk Kind = iota
	// KindPackageCache is a package manager's global download cache.
	KindPackageCache
	// KindWinTemp is Windows temporary files, crash dumps and web caches.
	KindWinTemp
	// KindEditorCache is an editor's or test runner's cache directory.
	KindEditorCache
	// KindLargeFile is a single file over the rule's size threshold.
	KindLargeFile
	// KindScoop is a Scoop app's superseded versions and download cache.
	KindScoop
	// KindDocker is reclaimable Docker storage, reached through the CLI.
	KindDocker
	// KindChoco is Chocolatey's package cache, which needs elevation.
	KindChoco
	// KindWinget is the winget download cache.
	KindWinget
)

// String returns the category's display word.
func (k Kind) String() string {
	switch k {
	case KindProjectJunk:
		return "Project junk"
	case KindPackageCache:
		return "Package cache"
	case KindWinTemp:
		return "Windows temp"
	case KindEditorCache:
		return "Editor cache"
	case KindLargeFile:
		return "Large file"
	case KindScoop:
		return "Scoop"
	case KindDocker:
		return "Docker"
	case KindChoco:
		return "Chocolatey"
	case KindWinget:
		return "winget"
	default:
		return "Unknown"
	}
}

// Item is one thing the scan found and the unit the results table, the
// confirmation dialog and the delete engine all work in.
//
// An Item is a value: it is safe to copy, it survives a gob round-trip, and
// it carries everything [Verify] needs to re-check the find immediately
// before anything is deleted. That last property is why Markers is stored on
// the Item rather than looked up in a rule table at delete time.
type Item struct {
	// Path is the absolute path of the directory or file to remove.
	Path string
	// Name is the base name of Path, kept so Verify can re-check it without
	// re-deriving Windows path semantics.
	Name string
	// Project is the absolute path of the directory that owns this item: the
	// project root for project junk, the parent directory otherwise. The
	// results table groups rows by it.
	Project string
	// Size is the number of bytes the item occupies locally, in uint64
	// because a node_modules tree can pass 4 GB.
	Size uint64
	// Tier is how much care deleting it needs.
	Tier Tier
	// Kind is the category it belongs to.
	Kind Kind
	// Rule is the name of the rule that matched, for display and for Verify.
	Rule string
	// RestoreHint says how to get the item back, in the user's words. It is
	// never empty; a test over the rule table pins that.
	RestoreHint string
	// Markers are the marker file names, any one of which must sit beside
	// Path for the item to still count as junk. Empty for rules that need no
	// marker.
	Markers []string
	// MarkersInside says the markers belong inside Path rather than beside
	// it, which is how Unity's Library and Temp folders are recognised.
	MarkersInside bool
	// LastUsed is the newest modification time among the project's top-level
	// files and its .git/index, or the item's own mtime when there is no
	// project around it.
	LastUsed time.Time
	// Active reports that LastUsed falls within the configured active window.
	// Active items are never pre-ticked.
	Active bool
	// Unverified reports that the rule wanted a marker file and none was
	// found. Unverified items are listed but never pre-ticked.
	Unverified bool
	// Cloud reports a cloud placeholder: nothing is stored locally, Size is
	// zero, and reading it would download it.
	Cloud bool
	// HardLinkedToStore reports that the tree contains hard links shared with
	// a package store, so deleting it frees less than Size suggests.
	HardLinkedToStore bool
	// ModTime is the item's own modification time.
	ModTime time.Time
}

// Stats are the running counters of a scan. Every field is cumulative and
// monotonic except Elapsed.
type Stats struct {
	// DirsChecked is how many directories the walker looked at.
	DirsChecked uint64
	// Found is how many items have been sent so far.
	Found uint64
	// Skipped is how many entries were deliberately not descended into:
	// filtered roots, reparse points, cloud placeholders and access denials.
	Skipped uint64
	// AccessDenied is the subset of Skipped that failed with a permission
	// error. It is reported separately because it is the user's to fix.
	AccessDenied uint64
	// Bytes is the total size of the items found so far.
	Bytes uint64
	// Elapsed is how long the scan has been running.
	Elapsed time.Duration
}

// Options configures a scan. The zero value is not usable: Roots must name at
// least one directory and Rules at least one rule.
type Options struct {
	// Roots are the directories to walk. Each must be an existing local
	// directory; UNC and mapped network paths are refused unless
	// AllowNetwork is set.
	Roots []string
	// NeverTouch is a list of absolute paths that are skipped outright,
	// together with everything beneath them. It comes from the user's
	// settings; this package takes it as a plain slice so it never has to
	// import internal/config.
	NeverTouch []string
	// IncludeOneDrive allows walking below the OneDrive folders. It is off by
	// default because reading a placeholder downloads it.
	IncludeOneDrive bool
	// AllowNetwork permits UNC and mapped network drives as roots.
	AllowNetwork bool
	// ActiveDays is how recently a project must have been touched to count as
	// active. Zero means seven.
	ActiveDays int
	// OlderDays drives the results screen's "older than" filter. The scanner
	// records it on the Stats-free path only; it is carried here so the UI
	// and the headless command read the same value from one place.
	OlderDays int
	// Rules is the rule table to match against, usually rules.Project() for
	// milestone 1 or rules.All() for a Full Scan.
	Rules []Rule
	// Workers is the number of parallel walk and size workers. Zero picks
	// min(32, 2*NumCPU).
	Workers int
	// DedupeHardLinks counts a hard-linked file once per scan by opening each
	// file for its file ID. It is off by default because the extra open per
	// file is expensive; the sizer turns it on by itself for a tree that
	// looks like a pnpm store.
	DedupeHardLinks bool
}

// Command describes a cleanup that is performed by running a tool's own
// command rather than by deleting files. Docker, Chocolatey and winget are
// cleaned this way because only the tool knows what is safe to remove.
type Command struct {
	// Name is the display name of the step.
	Name string
	// Kind is the category the step belongs to.
	Kind Kind
	// Tier is how much care running it needs.
	Tier Tier
	// Exe is the executable to run, looked up on PATH.
	Exe string
	// Args are the arguments, exactly as passed. They are a fixed list: no
	// part of Devpit ever builds them from user input.
	Args []string
	// ForbiddenArgs must never appear in Args. It is the machine-readable
	// form of "docker prune never takes --volumes"; a test asserts it.
	ForbiddenArgs []string
	// NeedsElevation reports that the command only works from the elevated
	// worker process.
	NeedsElevation bool
	// Description is the one-line explanation shown beside the step.
	Description string
	// RestoreHint says what has to be done to get the removed data back.
	RestoreHint string
}

// Valid reports whether the command's own safety constraints hold: it names
// an executable, explains itself, and contains none of its forbidden
// arguments.
func (c Command) Valid() error {
	switch {
	case c.Exe == "":
		return errEmptyCommandExe
	case c.RestoreHint == "":
		return errEmptyRestoreHint
	}
	for _, bad := range c.ForbiddenArgs {
		for _, arg := range c.Args {
			if strings.EqualFold(arg, bad) {
				return &ForbiddenArgError{Command: c.Name, Arg: bad}
			}
		}
	}
	return nil
}
