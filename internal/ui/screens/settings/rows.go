package settings

import (
	"fmt"
	"strconv"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/config"
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

// rowKind is how a row changes.
type rowKind int

const (
	// kindCycle steps through a list of values in place: ‹ auto ›.
	kindCycle rowKind = iota
	// kindToggle switches on and off in place.
	kindToggle
	// kindOpen opens a screen of its own: a trailing ›.
	kindOpen
	// kindNote is a quiet line that is never selected.
	kindNote
)

// row is one setting as the list draws it.
type row struct {
	id    string
	label string
	// value is the current value in words. A toggle's value is on.
	value string
	on    bool
	// quiet draws the value in the muted grey: "not set", "none".
	quiet bool
	// path marks a value that is a folder, shortened in the middle.
	path bool
	kind rowKind
	// desc says what the setting does and what its values mean, in plain
	// words for someone who has never opened Settings before.
	desc string
}

// group is a heading and its rows.
type group struct {
	title string
	rows  []row
}

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
		{"Look", []row{
			{
				id: rowTheme, label: "Theme", value: cfg.Theme, kind: kindCycle,
				desc: "The colours Devpit uses. auto follows your terminal, light or dark; dark and light pick one. " +
					"aqua, blue and rose change the accent colour, and mono uses no colour at all, only shapes and words.",
			},
			{
				id: rowIcons, label: "Icons", value: cfg.Icons, kind: kindCycle,
				desc: "The symbols Devpit draws. unicode works in every modern terminal. nerd adds file and tool icons " +
					"but needs the icon font below. ascii uses plain letters for old consoles. auto picks for you.",
			},
			{
				id: rowEmoji, label: "Emoji", on: cfg.Emoji, kind: kindToggle,
				desc: "An emoji now and then in titles and summaries, such as the flag on the welcome screen. " +
					"Never in lists or tables, so columns always line up.",
			},
			{
				id: rowFont, label: "Icon font", value: fontLabel(cfg), quiet: !cfg.FontInstalled, kind: kindOpen,
				desc: "Installs the free Symbols Nerd Font for your Windows user (no admin needed) and adds it to " +
					"Windows Terminal as a fallback, so nerd icons can draw. Your own font stays as it is.",
			},
			{
				id: rowProbe, label: "Icon check", value: probeLabel(cfg), quiet: !cfg.GlyphsConfirmed, kind: kindOpen,
				desc: "Shows a few icons and asks whether they look right. Nerd icons are only used after you say yes, " +
					"so a terminal that cannot draw them never shows boxes instead.",
			},
		}},
		{"Cleaning", []row{
			folderRow(cfg),
			{
				id: rowNeverT, label: "Never-touch folders", value: count(len(cfg.NeverTouch), "folder", "folders"),
				quiet: len(cfg.NeverTouch) == 0, kind: kindOpen,
				desc: "Folders Devpit never scans and never deletes from, whatever is inside them. " +
					"Devpit's own files and login folders such as .ssh and .claude are protected anyway.",
			},
			{
				id: rowActiveDays, label: "Recent projects", value: "last " + days(cfg.ActiveDays), kind: kindOpen,
				desc: fmt.Sprintf("A project you changed in the last %s counts as in use. Its junk is still listed but "+
					"never ticked for you, so you do not delete something you are working on.", days(cfg.ActiveDays)),
			},
			{
				id: rowOlderDays, label: "Age filter", value: "older than " + days(cfg.OlderDays), kind: kindOpen,
				desc: fmt.Sprintf("In the cleaning results, the age filter shows only folders nobody touched for more "+
					"than %s, so old leftovers stand out. This sets the number of days.", days(cfg.OlderDays)),
			},
			{
				id: rowRescan, label: "Forget last scan", kind: kindOpen,
				desc: "Devpit remembers the last scan so results show up at once next time. This clears that memory, " +
					"so the next scan reads the disk from scratch. Nothing on disk is deleted.",
			},
		}},
		{"Tools", []row{
			{
				id: rowManager, label: "Package manager", value: managerLabel(cfg.PreferredManager), kind: kindCycle,
				desc: "Which installer Install & Update uses for new apps. auto uses Scoop if you have it, " +
					"then winget, then Chocolatey. Pick one to always use it.",
			},
			{
				id: rowDevPorts, label: "Dev ports", value: count(len(cfg.DevPorts), "port", "ports"), kind: kindOpen,
				desc: "The ports Busy dev ports looks at in Ports & Network: the ones dev servers usually use, " +
					"such as 3000 to 3010, 5173 and 8080. Add your own, or go back to the defaults.",
			},
		}},
		{"AI agents", []row{skillRow(skill)}},
		{"Privacy and updates", []row{
			{
				id: rowTelemetry, label: "Usage stats", on: cfg.TelemetryOptIn, kind: kindToggle,
				desc: "When on, Devpit sends one small report after a cleanup: space freed and how many items. " +
					"Never a file name, a path or anything about you. Off unless you turn it on.",
			},
			{
				id: rowUpdates, label: "Update check", on: !cfg.SkipUpdateCheck, kind: kindToggle,
				desc: "When on, Devpit asks GitHub once a day whether a newer version is out, and says so at the top. " +
					"It sends nothing about you and never installs anything by itself.",
			},
		}},
		{"About", []row{
			{
				id: rowAbout, label: "About Devpit", value: "v" + ver, kind: kindOpen,
				desc: "The version you are running, who makes Devpit, links to the website and the source, " +
					"and the one command that updates your install.",
			},
			{
				id: rowWhatsNew, label: "What's new", value: whatsNewLabel(ver), kind: kindOpen,
				desc: "What changed in this version and where things moved: the same card Devpit shows once after an update.",
			},
		}},
	}
}

// folderRow is the projects folder: the path itself, shortened in the
// middle when long, with the whole of it in the description.
func folderRow(cfg config.Config) row {
	r := row{
		id: rowFolders, label: "Projects folder", kind: kindOpen, path: true,
		desc: "The folder Free Up Disk Space looks through first, usually where you keep your code. " +
			"It also lists the folders you scanned lately, so you can remove one.",
	}
	if cfg.DefaultProjectsFolder == "" {
		r.value, r.quiet, r.path = "not set", true, false
		return r
	}
	r.value = cfg.DefaultProjectsFolder
	r.desc += " Now: " + cfg.DefaultProjectsFolder
	return r
}

// skillRow is the AI agent skill: a row when Claude Code is on this PC, and
// one quiet line saying it is not otherwise. The line stays, rather than the
// group disappearing, so a person who installs Claude Code later knows
// where the skill will be.
func skillRow(s *skillSession) row {
	r := row{
		id: rowSkill, label: "AI agent skill", kind: kindOpen,
		desc: "Lets Claude Code use Devpit for you: check which account a folder uses and explain why. " +
			"It never changes anything without your yes. Open it to see the details, then install or remove it.",
	}
	switch {
	case s == nil || !s.loaded:
		r.value, r.quiet = "checking…", true
		return r
	case s.err != nil:
		r.value, r.quiet = "could not check", true
		r.desc = "Devpit could not look at Claude Code's skills folder. Open it to see why."
		return r
	case !s.status.ClaudeCode:
		return row{id: rowSkill, label: "Claude Code is not installed.", kind: kindNote}
	}
	switch s.status.Overall() {
	case service.AgentInstalled:
		r.value, r.on = "installed", true
	case service.AgentViaLink:
		r.value, r.on = "installed via a link", true
		r.desc = "Claude Code gets the Devpit skill through a shared skills folder, so nothing is written here. " +
			"It never changes anything without your yes. Open it to see where the skill comes from."
	case service.AgentOlder:
		r.value = "update available"
		r.desc = "The Devpit skill in Claude Code is older than this Devpit, or missing in one of your Claude Code " +
			"accounts. Open it to update: you see what is written where, and say yes first."
	case service.AgentInTheWay:
		r.value = "another devpit skill"
		r.desc = "Claude Code already has a skill called devpit that Devpit did not write, so Devpit will not " +
			"replace it. Open it to see where it is."
	default:
		r.value, r.quiet = "not installed", true
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
