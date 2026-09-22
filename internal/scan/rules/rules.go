// Package rules holds Devpit's table of what is reclaimable and what it
// costs to get back.
//
// The table is the safety-critical part of the scanner. Every entry names a
// marker file that proves the match is real: a folder called "build" is only
// build output when a build.gradle sits beside it, and a folder called
// "target" is only Rust output when a Cargo.toml does. The approach is
// kondo's, and it is the difference between a cleaner and an accident.
//
// Every rule carries a restore hint written for a person rather than a log
// file, because the confirmation dialog shows it and because a rule nobody
// can undo is a rule nobody should run. A test walks the whole table and
// fails the build if any hint is empty.
//
// The package deliberately does not import anything but internal/scan: it is
// data, not behaviour.
package rules

import "github.com/zubairbinshaukat/devpit/internal/scan"

// Project returns the rules for the Project Junk scan: build output and
// dependency folders found by walking the user's code folders. This is the
// milestone 1 set and the one the Clean screen uses by default.
func Project() []scan.Rule {
	out := make([]scan.Rule, 0, 48)
	out = append(out, nodeRules()...)
	out = append(out, rustRules()...)
	out = append(out, dotnetRules()...)
	out = append(out, pythonRules()...)
	out = append(out, jvmRules()...)
	out = append(out, otherLanguageRules()...)
	return out
}

// All returns every rule a Full Scan uses: the project rules plus the package
// manager caches, Windows temporary files, editor caches, large files and
// Scoop's superseded versions.
//
// Command-based cleanups are not here. Docker and Chocolatey are cleaned by
// running their own tools, not by deleting their files, so they come back
// from Commands instead.
func All() []scan.Rule {
	out := Project()
	out = append(out, Caches()...)
	out = append(out, WinTemp()...)
	out = append(out, Editors()...)
	out = append(out, LargeFiles()...)
	out = append(out, Scoop()...)
	out = append(out, Winget()...)
	return out
}

// Commands returns the cleanups performed by running a tool's own command.
// Only the tool knows which of its layers are still referenced, so Devpit
// asks it rather than guessing at its storage.
func Commands() []scan.Command {
	out := make([]scan.Command, 0, 6)
	out = append(out, Docker()...)
	out = append(out, Choco()...)
	return out
}
