package host

import (
	"os"
	"path/filepath"

	"github.com/zubairbinshaukat/devpit/internal/config"
)

// StateDir is where the manifest of a running share and the record of an
// interrupted copy live: a share folder inside Devpit's own settings
// directory, which DEVPIT_CONFIG_DIR relocates like everything else. When no
// settings directory can be found it falls back to a folder in the system
// temp directory, so a share is never started without a place to record it.
func StateDir() string {
	if d, err := config.Dir(); err == nil {
		return filepath.Join(d, "share")
	}
	return filepath.Join(os.TempDir(), "devpit-share")
}
