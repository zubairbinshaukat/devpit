package devpit

import (
	"fmt"
	"io"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/fonts/setup"
)

// Line glyphs for the font subcommands. They are fixed rather than picked
// from the TUI's icon tiers: this output is read by web/install.ps1 (which
// keys its own styling off the first rune) as much as by people, and a
// script needs the same three markers on every machine.
const (
	glyphOK   = "✔"
	glyphNote = "•"
	glyphWarn = "!"
)

// windowsOnlyLine is every font subcommand's whole output off Windows. It is
// a note with exit code 0, not an error: `devpit font install` run from a
// cross-platform dotfiles script has nothing to do there, not something to
// fail on.
const windowsOnlyLine = glyphNote + " Icon font install is Windows-only"

// fontEnv is everything the font subcommands reach outside the process
// through, so tests can drive every outcome, including the config
// bookkeeping, with fakes and never touch the real registry, network,
// Windows Terminal settings or config file.
type fontEnv struct {
	deps       setup.Deps
	windows    bool
	loadConfig func() (config.Result, error)
	saveConfig func(config.Config) error
}

// defaultFontEnv is the real environment: setup's default engine calls and
// the same config loader and saver the TUI uses.
func defaultFontEnv() fontEnv {
	return fontEnv{
		windows:    runtime.GOOS == "windows",
		loadConfig: config.Load,
		saveConfig: config.Save,
	}
}

// newFontCmd manages the icon font from a script. web/install.ps1 runs
// `devpit font install --quiet` right after installing devpit.exe, so the
// icons are there on first launch without a trip to Settings.
func newFontCmd() *cobra.Command { return newFontCmdWith(defaultFontEnv()) }

// newFontCmdWith builds the font command tree against env. Every
// subcommand is non-interactive and exits 0 for every outcome a user can
// expect (offline, blocked by policy, not on Windows, nothing to remove),
// so an installer that runs it can never be failed by it; only a genuinely
// unexpected error returns non-nil, which Execute turns into exit code 1
// with the message on stderr.
func newFontCmdWith(env fontEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "font",
		Short: "Install, remove or check the icon font",
		Long: "Installs Symbols Nerd Font Mono for the current user (no admin needed) and adds it " +
			"to Windows Terminal as a fallback font, so your own font is kept. " +
			"The same as Settings › Icon font.",
		Args: cobra.NoArgs,
	}

	var quiet bool
	install := &cobra.Command{
		Use:   "install",
		Short: "Install Symbols Nerd Font Mono for the current user",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			if !env.windows {
				return printLines(w, []string{windowsOnlyLine})
			}
			cfg, unsaved := env.load()
			out, err := setup.Install(cmd.Context(), env.deps)
			if err != nil {
				return fmt.Errorf("devpit font install: %w", err)
			}
			// The config is saved in quiet mode too; only the note saying
			// it could not be is dropped, since quiet promises one line.
			note := env.persist(out, cfg, unsaved)
			lines := installLines(out, quiet)
			if !quiet {
				lines = append(lines, note...)
			}
			return printLines(w, lines)
		},
	}
	install.Flags().BoolVarP(&quiet, "quiet", "q", false, "print only the one-line result")

	remove := &cobra.Command{
		Use:   "remove",
		Short: "Remove the icon font and undo the terminal patch",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			if !env.windows {
				return printLines(w, []string{windowsOnlyLine})
			}
			cfg, unsaved := env.load()
			out, err := setup.Remove(env.deps)
			if err != nil {
				return fmt.Errorf("devpit font remove: %w", err)
			}
			return printLines(w, append(removeLines(out), env.persist(out, cfg, unsaved)...))
		},
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Report whether the icon font is installed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			if !env.windows {
				return printLines(w, []string{windowsOnlyLine})
			}
			st, err := setup.Status(env.deps)
			if err != nil {
				return fmt.Errorf("devpit font status: %w", err)
			}
			if st.Installed {
				return printLines(w, []string{glyphOK + " Icon font installed (" + st.FontPath + ")"})
			}
			return printLines(w, []string{glyphNote + " Icon font not installed"})
		},
	}

	cmd.AddCommand(install, remove, status)
	return cmd
}

// load reads the config before the font is touched, and says why it must
// not be saved afterwards if that is the case. A config that failed to load,
// or that Load could only replace with defaults while leaving the unusable
// file in place (a newer schema, or a quarantine that failed), is never
// written back: saving defaults over it would throw away every other
// setting just to record one flag. The font is installed or removed
// regardless; only the bookkeeping is skipped.
func (e fontEnv) load() (cfg config.Config, unsaved string) {
	res, err := e.loadConfig()
	switch {
	case err != nil:
		return res.Config, err.Error()
	case res.Warning != "" && !res.Created:
		return res.Config, res.Warning
	}
	return res.Config, ""
}

// persist saves out's config change when the run succeeded and the config
// is safe to write, and returns the line telling the user when it was not
// saved. Nothing is written for an outcome that changed nothing (offline,
// blocked by policy).
func (e fontEnv) persist(out setup.Outcome, cfg config.Config, unsaved string) []string {
	if !out.OK() {
		return nil
	}
	if unsaved == "" {
		if err := e.saveConfig(out.Apply(cfg)); err != nil {
			unsaved = err.Error()
		}
	}
	if unsaved != "" {
		return []string{glyphWarn + " Setting not saved: " + unsaved}
	}
	return nil
}

// installLines renders a setup.Install outcome: a one-line result, then
// (unless quiet) one indented line per Windows Terminal settings.json.
// quiet exists for web/install.ps1, which shows exactly one line per step;
// a policy block folds its manual steps into that line so they are not lost.
func installLines(out setup.Outcome, quiet bool) []string {
	var head string
	switch out.Kind {
	case setup.Offline:
		return []string{glyphNote + " Skipped: no internet. Install later from Settings › Icon font."}
	case setup.PolicyBlocked:
		if quiet {
			return []string{glyphWarn + " Blocked by policy: " + out.ManualSteps}
		}
		return []string{glyphWarn + " Blocked by policy:", "  " + out.ManualSteps}
	case setup.AlreadyInstalled:
		head = glyphOK + " Icon font already installed"
	default:
		head = glyphOK + " Icon font installed (" + setup.FallbackFont + ")"
	}
	if quiet {
		return []string{head}
	}

	lines := []string{head}
	if len(out.Terminals) == 0 {
		return append(lines, "  "+glyphNote+` Windows Terminal not found; set "`+setup.FallbackFont+
			`" as a fallback font in your terminal`)
	}
	for _, t := range out.Terminals {
		switch {
		case t.Err != nil:
			lines = append(lines, fmt.Sprintf(`  %s Windows Terminal: not changed, add "%s" to profiles.defaults.font.face yourself (%v)`,
				glyphWarn, setup.FallbackFont, t.Err))
		case t.Changed:
			lines = append(lines, "  "+glyphOK+" Windows Terminal: added as fallback font ("+t.Location.Path+")")
		default:
			lines = append(lines, "  "+glyphOK+" Windows Terminal: fallback font already set ("+t.Location.Path+")")
		}
	}
	return lines
}

// removeLines renders a setup.Remove outcome. A settings.json Devpit never
// backed up is left out: nothing happened to it, and listing it would read
// as though something had.
func removeLines(out setup.Outcome) []string {
	if out.Kind == setup.NotInstalled {
		return []string{glyphNote + " Icon font not installed; nothing to remove"}
	}
	lines := []string{glyphOK + " Icon font removed"}
	for _, t := range out.Terminals {
		switch {
		case t.Err != nil:
			lines = append(lines, fmt.Sprintf("  %s Windows Terminal: could not restore %s (%v)", glyphWarn, t.Location.Path, t.Err))
		case t.Changed:
			lines = append(lines, "  "+glyphOK+" Windows Terminal: original settings restored ("+t.Location.Path+")")
		}
	}
	return lines
}

// printLines writes lines to w, one per line. A failed write is returned so
// a closed pipe still ends in a non-zero exit rather than silent success.
func printLines(w io.Writer, lines []string) error {
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}
