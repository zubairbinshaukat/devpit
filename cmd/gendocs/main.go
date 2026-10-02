// Command gendocs writes the Command line page of the documentation site from
// the real Cobra command tree. It is a maintainer tool run through
// `task docs:cli`, never shipped: GoReleaser builds only the root package.
//
// It lives beside cmd/devpit rather than inside it so it is not a hidden
// subcommand of the app users install, and so it can import the command tree
// the same way main.go does.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	devpit "github.com/zubairbinshaukat/devpit/cmd/devpit"
	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/shims"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/docsgen"
	"github.com/zubairbinshaukat/devpit/internal/selfupdate"
	"github.com/zubairbinshaukat/devpit/internal/telemetry"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// defaultOut is where the page lives, relative to the repository root. The
// docs site reads it as a normal content file.
const defaultOut = "web/docs-site/src/content/docs/command-line.mdx"

// Page renders the current Command line page. The environment variable names
// come from the constants of the packages that read them, so renaming one in
// code changes the page on the next run instead of leaving it stale.
func Page() string {
	return docsgen.Render(devpit.NewRootCmd(), docsgen.Options{
		Env: []docsgen.EnvVar{
			{Name: telemetry.EnvNoTelemetry, Effect: "Set to 1 to switch usage stats off, whatever the setting says."},
			{Name: telemetry.EnvDoNotTrack, Effect: "The common Do Not Track switch. Set to 1 to switch usage stats off."},
			{Name: selfupdate.EnvNoCheck, Effect: "Set to 1 to skip the once-a-day check for a newer Devpit."},
			{Name: config.EnvConfigDir, Effect: "Use this folder for config.toml instead of %APPDATA%\\devpit."},
			{Name: config.EnvCacheDir, Effect: "Use this folder for caches and logs instead of %LOCALAPPDATA%\\devpit."},
			// The two Accounts locations. They are mainly for tests, but a
			// portable install that moves the settings folder needs them too,
			// and the PATH side effect of the shim folder is worth stating.
			{Name: accounts.EnvAccountsDir, Effect: "Use this folder for the account folders Devpit creates instead of %USERPROFILE%\\.devpit\\accounts. Devpit's Git rule files move with it, to a git folder beside it. Meant for tests and portable installs."},
			{Name: shims.EnvShimDir, Effect: "Use this folder for the account shims (claude.exe, gh.exe and the rest) instead of %LOCALAPPDATA%\\Programs\\devpit\\shims. While it is set, Devpit does not change your user PATH, so put that folder first on PATH yourself. Meant for tests and portable installs."},
			{Name: uictx.ReducedMotionEnv, Effect: "Turn off spinners and other movement in the app."},
		},
	})
}

// main writes the page to the path given as the first argument, or to
// defaultOut when there is none. Run it from the repository root.
func main() {
	out := defaultOut
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil { //nolint:gosec // G703: a developer-run tool, the path is its own argument
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, []byte(Page()), 0o600); err != nil { //nolint:gosec // G703: a developer-run tool, the path is its own argument
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", out)
}
