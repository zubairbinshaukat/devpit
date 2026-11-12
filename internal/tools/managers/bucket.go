package managers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/tools/catalog"
)

// KnownBuckets is the only place a Scoop bucket URL can come from: the
// buckets Devpit may add, by name. A catalog entry's bucket must match one of
// these (same name, same URL) or [InstallPlan] refuses it. A bucket is never
// built from typed input.
func KnownBuckets() map[string]string {
	return map[string]string{
		"xevrion": "https://github.com/xevrion/scoop-bucket",
	}
}

// ErrUnknownBucket is returned by [InstallPlan] when an app's bucket is not
// in [KnownBuckets] with the same URL, or cannot be read at all.
var ErrUnknownBucket = errors.New("managers: bucket is not in Devpit's list of known buckets")

// BucketAddCmd implements the Scoop step that adds a bucket:
// `scoop bucket add <name> <url>`. The caller passes a name and URL taken
// from [KnownBuckets]; see [InstallPlan].
func (Scoop) BucketAddCmd(name, url string) []string {
	return []string{"scoop", "bucket", "add", name, url}
}

// BucketAlreadyAdded reports whether the output of `scoop bucket add` says
// the bucket is already there ("The 'xevrion' bucket already exists."),
// which counts as the bucket being added. Any case, and whatever the exit
// code: Scoop has used both 0 and 1 for it across versions.
func BucketAlreadyAdded(_ int, lines []string) bool {
	for _, l := range lines {
		if strings.Contains(strings.ToLower(l), "already exists") {
			return true
		}
	}
	return false
}

// Step is one command of an app's install: an argv to run, how to tell it
// worked, and a short phrase for the screen.
type Step struct {
	// Argv is the exact command, program first.
	Argv []string
	// OKIfLine, when set, is asked about a step that did not exit 0 and
	// can turn it into a success (Scoop's "bucket already exists"). Nil
	// means exit 0 only.
	OKIfLine func(exit int, lines []string) bool
	// Label is what the screen says is happening: "Adding the xevrion
	// bucket", "Installing hatchzip".
	Label string
}

// InstallPlan is the ordered steps that install app under m. Without a
// bucket, or under any manager but Scoop, it is the single
// [Manager.InstallCmd] step, argv unchanged. For Scoop with a bucket it is
// the bucket add, then the install. [ErrUnknownBucket] if the catalog's
// bucket is not in [KnownBuckets] with the same URL.
func InstallPlan(m Manager, app catalog.App) ([]Step, error) {
	id := installID(m.Name(), app)
	install := Step{Argv: m.InstallCmd(id), Label: "Installing " + app.Name}
	s, isScoop := m.(Scoop)
	if !isScoop || app.Bucket == "" {
		return []Step{install}, nil
	}
	name, url, ok := app.ScoopBucket()
	if !ok || KnownBuckets()[name] != url {
		return nil, fmt.Errorf("%w: %q", ErrUnknownBucket, app.Bucket)
	}
	return []Step{
		{
			Argv:     s.BucketAddCmd(name, url),
			OKIfLine: BucketAlreadyAdded,
			Label:    "Adding the " + name + " bucket",
		},
		install,
	}, nil
}

// installID is the catalog's id for app under the named manager.
func installID(manager string, a catalog.App) string {
	switch manager {
	case "scoop":
		return a.Scoop
	case "winget":
		return a.Winget
	case "choco":
		return a.Choco
	default:
		return ""
	}
}
