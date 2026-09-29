package managers

import "strings"

// Retry says how to run again, by hand, an app whose update or install was
// stopped or failed.
type Retry struct {
	// Command is the exact line to type, e.g. "winget upgrade --id Git.Git -e ...".
	Command string
	// Note is extra advice in plain words, or "". For npm it says the package
	// may need installing again.
	Note string
}

// String is the retry as one sentence-ready text: the command, then the
// note.
func (r Retry) String() string {
	if r.Note == "" {
		return r.Command
	}
	return r.Command + ". " + r.Note
}

// RetryFor builds the retry for one app of manager. id is the manager's id
// for it; install picks the install command over the upgrade one. all is the
// manager's "update everything" fallback, which has no single id.
//
// Stopping an installer partway is safe for winget, Scoop and Chocolatey to
// roll back from, in most cases. npm replaces a global package in place, so
// a package stopped in the middle can be missing altogether, which is why its
// note says it may need installing again.
func RetryFor(m Manager, id string, install, all bool) Retry {
	var argvs [][]string
	switch {
	case all:
		argvs = m.UpgradeAllCmds()
	case install:
		argvs = [][]string{m.InstallCmd(id)}
	default:
		argvs = [][]string{m.UpgradeCmd(id)}
	}
	lines := make([]string, len(argvs))
	for i, a := range argvs {
		lines[i] = strings.Join(a, " ")
	}
	r := Retry{Command: strings.Join(lines, " && ")}
	switch m.Name() {
	case "npm":
		if !all {
			r.Note = "npm may have removed it before it stopped. If " + id + " no longer runs, install it again with: " +
				strings.Join(m.InstallCmd(id), " ")
		}
	case "choco":
		r.Note = "Run it in a terminal opened as administrator."
	}
	return r
}
