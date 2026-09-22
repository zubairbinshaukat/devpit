package managers

import "encoding/json"

// NPM drives npm's global package installs (node_modules alongside the
// Node.js install itself, not a project's local dependencies). It is never
// picked as the machine's [Preferred] manager; it always runs as its own
// Update step, since global npm packages exist independently of whichever
// of Scoop, winget or Chocolatey installed Node itself.
type NPM struct{}

// Name implements [Manager].
func (NPM) Name() string { return "npm" }

// ListCmd implements [Manager].
func (NPM) ListCmd() []string { return []string{"npm", "ls", "-g", "--json", "--depth=0"} }

// OutdatedCmd implements [Manager].
func (NPM) OutdatedCmd() []string { return []string{"npm", "outdated", "-g", "--json"} }

// UpgradeAllCmds implements [Manager].
func (NPM) UpgradeAllCmds() [][]string { return [][]string{{"npm", "update", "-g"}} }

// InstallCmd implements [Manager].
func (NPM) InstallCmd(id string) []string { return []string{"npm", "install", "-g", id} }

// NeedsElevation implements [Manager]. npm's global prefix lives under the
// current user's Node.js install on Windows, so it never needs elevation.
func (NPM) NeedsElevation() bool { return false }

// npmListOutput mirrors the shape of "npm ls -g --json --depth=0":
//
//	{
//	  "dependencies": {
//	    "npm":  {"version": "10.2.4"},
//	    "pnpm": {"version": "8.15.1"}
//	  }
//	}
type npmListOutput struct {
	Dependencies map[string]struct {
		Version string `json:"version"`
	} `json:"dependencies"`
}

// ParseList implements [Manager]. Invalid or empty JSON parses to no
// packages rather than an error, matching every other manager's lenient
// parsing here.
func (NPM) ParseList(out string) []Installed {
	var parsed npmListOutput
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil
	}
	if len(parsed.Dependencies) == 0 {
		return nil
	}
	result := make([]Installed, 0, len(parsed.Dependencies))
	for name, dep := range parsed.Dependencies {
		result = append(result, Installed{Name: name, ID: name, Version: dep.Version})
	}
	sortInstalled(result)
	return result
}

// npmOutdatedEntry mirrors one value of "npm outdated -g --json"'s object,
// keyed by package name:
//
//	{
//	  "npm": {"current": "10.2.0", "wanted": "10.2.4", "latest": "10.2.4"}
//	}
type npmOutdatedEntry struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
}

// ParseOutdated implements [Manager]. When everything is current, npm
// prints nothing at all (empty stdout, exit code 0) rather than "{}"; both
// parse to no packages.
func (NPM) ParseOutdated(out string) []Outdated {
	var parsed map[string]npmOutdatedEntry
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil
	}
	result := make([]Outdated, 0, len(parsed))
	for name, entry := range parsed {
		result = append(result, Outdated{
			Name:    name,
			ID:      name,
			Current: entry.Current,
			Latest:  entry.Latest,
		})
	}
	sortOutdated(result)
	return result
}
