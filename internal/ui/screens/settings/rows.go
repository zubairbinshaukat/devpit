package settings

import (
	"fmt"
	"strconv"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/choices"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/whatsnew"
)

// Row identifiers.
const (
	rowTheme      = "theme"
	rowIcons      = "icons"
	rowEmoji      = "emoji"
	rowFont       = "font"
	rowProbe      = "probe"
	rowFolders    = "folders"
	rowNeverT     = "never_touch"
	rowActiveDays = "active_days"
	rowOlderDays  = "older_days"
	rowRescan     = "rescan"
	rowManager    = "manager"
	rowDevPorts   = "dev_ports"
	rowSkill      = "skill"
	rowTelemetry  = "telemetry"
	rowUpdates    = "updates"
	rowAbout      = "about"
	rowWhatsNew   = "whats_new"
)

// row is one setting as the list draws it, and group a heading and its
// rows: the shared choices component's own types.
type (
	row   = choices.Item
	group = choices.Group
)

// The ways a row changes, in the component's words.
const (
	kindCycle  = choices.Cycles
	kindToggle = choices.Toggles
	kindOpen   = choices.Opens
	kindNote   = choices.Note
)

// iconTiers, themes and managers are the cycle orders for the in-place enum
// settings. managers starts with "", which Preferred (internal/tools/
// managers) already treats as "auto": try Scoop, then winget, then
// Chocolatey.
var (
	iconTiers = []string{config.IconsAuto, config.IconsNerd, config.IconsUnicode, config.IconsASCII}
	themes    = config.Themes
	managers  = []string{"", "scoop", "winget", "choco"}
)

// groups builds the list from the configuration and what is known about the
// skill, so the screen always shows real values. The most used settings come
// first.
func groups(cfg config.Config, skill *skillSession, ver string) []group {
	return []group{
		{Title: "Look", Items: []row{
			{
				ID: rowTheme, Label: "Theme", Value: cfg.Theme, Kind: kindCycle,
				Desc: "The colours Devpit uses. auto follows your terminal, light or dark; dark and light pick one. " +
					"aqua, blue and rose change the accent colour, and mono uses no colour at all, only shapes and words.",
			},
			{
				ID: rowIcons, Label: "Icons", Value: cfg.Icons, Kind: kindCycle,
				Desc: "The symbols Devpit draws. unicode works in every modern terminal. nerd adds file and tool icons " +
					"but needs the icon font below. ascii uses plain letters for old consoles. auto picks for you.",
			},
			{
				ID: rowEmoji, Label: "Emoji", On: cfg.Emoji, Kind: kindToggle,
				Desc: "An emoji now and then in titles and summaries, such as the flag on the welcome screen. " +
					"Never in lists or tables, so columns always line up.",
			},
			{
				ID: rowFont, Label: "Icon font", Value: fontLabel(cfg), Quiet: !cfg.FontInstalled, Kind: kindOpen,
				Desc: "Installs the free Symbols Nerd Font for your Windows user (no admin needed) and adds it to " +
					"Windows Terminal as a fallback, so nerd icons can draw. Your own font stays as it is.",
			},
			{
				ID: rowProbe, Label: "Icon check", Value: probeLabel(cfg), Quiet: !cfg.GlyphsConfirmed, Kind: kindOpen,
				Desc: "Shows a few icons and asks whether they look right. Nerd icons are only used after you say yes, " +
					"so a terminal that cannot draw them never shows boxes instead.",
			},
		}},
		{Title: "Cleaning", Items: []row{
			folderRow(cfg),
			{
				ID: rowNeverT, Label: "Never-touch folders", Value: count(len(cfg.NeverTouch), "folder", "folders"),
				Quiet: len(cfg.NeverTouch) == 0, Kind: kindOpen,
				Desc: "Folders Devpit never scans and never deletes from, whatever is inside them. " +
					"Devpit's own files and login folders such as .ssh and .claude are protected anyway.",
			},
			{
				ID: rowActiveDays, Label: "Recent projects", Value: "last " + days(cfg.ActiveDays), Kind: kindOpen,
				Desc: fmt.Sprintf("A project you changed in the last %s counts as in use. Its junk is still listed but "+
					"never ticked for you, so you do not delete something you are working on.", days(cfg.ActiveDays)),
			},
			{
				ID: rowOlderDays, Label: "Age filter", Value: "older than " + days(cfg.OlderDays), Kind: kindOpen,
				Desc: fmt.Sprintf("In the cleaning results, the age filter shows only folders nobody touched for more "+
					"than %s, so old leftovers stand out. This sets the number of days.", days(cfg.OlderDays)),
			},
			{
				ID: rowRescan, Label: "Forget last scan", Kind: kindOpen,
				Desc: "Devpit remembers the last scan so results show up at once next time. This clears that memory, " +
					"so the next scan reads the disk from scratch. Nothing on disk is deleted.",
			},
		}},
		{Title: "Tools", Items: []row{
			{
				ID: rowManager, Label: "Package manager", Value: managerLabel(cfg.PreferredManager), Kind: kindCycle,
				Desc: "Which installer Install & Update uses for new apps. auto uses Scoop if you have it, " +
					"then winget, then Chocolatey. Pick one to always use it.",
			},
			{
				ID: rowDevPorts, Label: "Dev ports", Value: count(len(cfg.DevPorts), "port", "ports"), Kind: kindOpen,
				Desc: "The ports Busy dev ports looks at in Ports & Network: the ones dev servers usually use, " +
					"such as 3000 to 3010, 5173 and 8080. Add your own, or go back to the defaults.",
			},
		}},
		{Title: "AI agents", Items: []row{skillRow(skill)}},
		{Title: "Privacy and updates", Items: []row{
			{
				ID: rowTelemetry, Label: "Usage stats", On: cfg.TelemetryOptIn, Kind: kindToggle,
				Desc: "When on, Devpit sends one small report after a cleanup: space freed and how many items. " +
					"Never a file name, a path or anything about you. Off unless you turn it on.",
			},
			{
				ID: rowUpdates, Label: "Update check", On: !cfg.SkipUpdateCheck, Kind: kindToggle,
				Desc: "When on, Devpit asks GitHub once a day whether a newer version is out, and says so at the top. " +
					"It sends nothing about you and never installs anything by itself.",
			},
		}},
		{Title: "About", Items: []row{
			{
				ID: rowAbout, Label: "About Devpit", Value: header.VersionLabel(ver), Kind: kindOpen,
				Desc: "The version you are running, who makes Devpit, links to the website and the source, " +
					"and the one command that updates your install.",
			},
			{
				ID: rowWhatsNew, Label: "What's new", Value: whatsNewLabel(ver), Kind: kindOpen,
				Desc: "What changed in this version and where things moved: the same card Devpit shows once after an update.",
			},
		}},
	}
}

// folderRow is the projects folder: the path itself, shortened in the
// middle when long, with the whole of it in the description.
func folderRow(cfg config.Config) row {
	r := row{
		ID: rowFolders, Label: "Projects folder", Kind: kindOpen, Path: true,
		Desc: "The folder Free Up Disk Space looks through first, usually where you keep your code. " +
			"It also lists the folders you scanned lately, so you can remove one.",
	}
	if cfg.DefaultProjectsFolder == "" {
		r.Value, r.Quiet, r.Path = "not set", true, false
		return r
	}
	r.Value = cfg.DefaultProjectsFolder
	r.Desc += " Now: " + cfg.DefaultProjectsFolder
	return r
}

// skillRow is the AI agent skill: a row when Claude Code is on this PC, and
// one quiet line saying it is not otherwise. The line stays, rather than the
// group disappearing, so a person who installs Claude Code later knows
// where the skill will be.
func skillRow(s *skillSession) row {
	r := row{
		ID: rowSkill, Label: "AI agent skill", Kind: kindOpen,
		Desc: "Lets Claude Code use Devpit for you: check which account a folder uses and explain why. " +
			"It never changes anything without your yes. Open it to see the details, then install or remove it.",
	}
	switch {
	case s == nil || !s.loaded:
		r.Value, r.Quiet = "checking…", true
		return r
	case s.err != nil:
		r.Value, r.Quiet = "could not check", true
		r.Desc = "Devpit could not look at Claude Code's skills folder. Open it to see why."
		return r
	case !s.status.ClaudeCode:
		return row{ID: rowSkill, Label: "Claude Code is not installed.", Kind: kindNote}
	}
	switch s.status.Overall() {
	case service.AgentInstalled:
		r.Value, r.On = "installed", true
	case service.AgentViaLink:
		r.Value, r.On = "installed via a link", true
		r.Desc = "Claude Code gets the Devpit skill through a shared skills folder, so nothing is written here. " +
			"It never changes anything without your yes. Open it to see where the skill comes from."
	case service.AgentOlder:
		r.Value = "update available"
		r.Desc = "The Devpit skill in Claude Code is older than this Devpit, or missing in one of your Claude Code " +
			"accounts. Open it to update: you see what is written where, and say yes first."
	case service.AgentInTheWay:
		r.Value = "another devpit skill"
		r.Desc = "Claude Code already has a skill called devpit that Devpit did not write, so Devpit will not " +
			"replace it. Open it to see where it is."
	default:
		r.Value, r.Quiet = "not installed", true
	}
	return r
}

// count is "none", "1 folder", "27 ports".
func count(n int, one, many string) string {
	switch n {
	case 0:
		return "none"
	case 1:
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// days is "1 day", "7 days".
func days(n int) string {
	if n == 1 {
		return "1 day"
	}
	return strconv.Itoa(n) + " days"
}

// managerLabel renders the preferred-manager value the way the UI words it;
// the empty string is "auto".
func managerLabel(m string) string {
	switch m {
	case "":
		return "auto"
	case "scoop":
		return "Scoop"
	case "choco":
		return "Chocolatey"
	}
	return m
}

// fontLabel is the one-line value shown on the font row.
func fontLabel(cfg config.Config) string {
	if cfg.FontInstalled {
		return "installed"
	}
	return "not installed"
}

// whatsNewLabel is the release the What's new card is about.
func whatsNewLabel(ver string) string {
	if e, ok := whatsnew.For(ver); ok {
		return "in " + e.Version
	}
	if rs := whatsnew.Releases(); len(rs) > 0 {
		return "in " + rs[0].Version
	}
	return ""
}

// whatsNewEntry is the card the What's new row opens: the running version's,
// or the newest in the table for a development build.
func whatsNewEntry(ver string) whatsnew.Entry {
	if e, ok := whatsnew.For(ver); ok {
		return e
	}
	if rs := whatsnew.Releases(); len(rs) > 0 {
		return rs[0]
	}
	return whatsnew.Entry{}
}
