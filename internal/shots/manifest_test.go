//go:build shots

package shots

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// entry is one line of the screenshot manifest. It mirrors the JSON schema
// documented in web/docs-site/CONTRACT.md.
type entry struct {
	// Name is the file name of the image, without extension. It is unique
	// across both manifest files.
	Name string `json:"name"`
	// Alt is the alternative text the docs page carries for the image.
	Alt string `json:"alt"`
	// Screen and State name the scene to render, as "screen/state".
	Screen string `json:"screen"`
	State  string `json:"state"`
	// Cols and Rows are the terminal size in cells.
	Cols int `json:"cols"`
	Rows int `json:"rows"`
	// Theme is "dark" (the default) or "light".
	Theme string `json:"theme"`
	// Note is a hint to whoever maintains the scene; it is never rendered.
	Note string `json:"note"`
}

// key is the scene registry key of an entry.
func (e entry) key() string { return e.Screen + "/" + e.State }

// docsSite is the docs site folder, relative to this package.
const docsSite = "../../web/docs-site"

// loadManifest reads every shots*.json file next to the docs site's
// package.json, in file-name order, and returns the entries in file order.
func loadManifest(dir string) ([]entry, error) {
	files, err := filepath.Glob(filepath.Join(dir, "shots*.json"))
	if err != nil {
		return nil, fmt.Errorf("listing the manifests: %w", err)
	}
	sort.Strings(files)

	var all []entry
	seen := map[string]string{}
	for _, f := range files {
		raw, err := os.ReadFile(f) //nolint:gosec // a manifest inside the repository.
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		var part []entry
		if err := json.Unmarshal(raw, &part); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", f, err)
		}
		for _, e := range part {
			if prev, dup := seen[e.Name]; dup {
				return nil, fmt.Errorf("the name %q is in both %s and %s", e.Name, prev, filepath.Base(f))
			}
			seen[e.Name] = filepath.Base(f)
			if e.Theme == "" {
				e.Theme = "dark"
			}
			if e.Theme != "dark" && e.Theme != "light" {
				return nil, fmt.Errorf("%s: theme %q must be dark or light", e.Name, e.Theme)
			}
			if strings.TrimSpace(e.Screen) == "" || strings.TrimSpace(e.State) == "" {
				return nil, fmt.Errorf("%s: screen and state are required", e.Name)
			}
			all = append(all, e)
		}
	}
	return all, nil
}
