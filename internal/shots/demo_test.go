//go:build shots

package shots

import (
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The demo machine. Every value on a screenshot comes from here or from a
// scene's own fakes, and none of it is real: the user is a made-up developer,
// the projects are made-up projects, and the addresses are the private and
// documentation ranges (192.168.x.x, 203.0.113.x, RFC 5737) that no real
// host owns.
const (
	// demoVersion is the version the screenshots say they were taken from.
	demoVersion = "0.4.0"
	// demoNode and demoGit fill the header's tool pills.
	demoNode = "22.11.0"
	demoGit  = "2.47.1"
	// demoDiskFree and demoDiskTotal fill the header's disk pill: 214 GB free
	// of a 476 GB drive.
	demoDiskFree  = 214 << 30
	demoDiskTotal = 476 << 30
	// demoUser is the Windows user name in every path.
	demoUser = "alex"
	// demoProjects is the default projects folder.
	demoProjects = `D:\projects`
	// demoName and demoEmail are the git identity.
	demoName  = "Alex Morgan"
	demoEmail = "alex.morgan@example.com"
)

// demoConfig is a settled configuration for a developer who has run Devpit
// before: past first run, with the icon font installed and confirmed, so
// every screenshot shows the Nerd Font icons the docs tell people to install.
func demoConfig() config.Config {
	cfg := config.Default()
	cfg.FirstRunDone = true
	cfg.Icons = config.IconsNerd
	cfg.FontInstalled = true
	cfg.GlyphsConfirmed = true
	cfg.Emoji = false
	cfg.Theme = config.ThemeAuto
	cfg.DefaultProjectsFolder = demoProjects
	cfg.RecentFolders = []string{demoProjects, `D:\work\client-sites`, `C:\Users\` + demoUser + `\source\repos`}
	cfg.NeverTouch = []string{`D:\projects\client-billing`, `C:\Users\` + demoUser + `\Documents`}
	cfg.LifetimeFreedBytes = 31_600_000_000
	return cfg
}

// scene drives a session to the state one manifest entry asks for.
type scene func(t *testing.T, e entry) *session

// screens is the map a home menu section opens, keyed by home.Section*.
type screens = map[string]func() uictx.Screen

// homeKeys are the digits that open each of the six home sections.
const (
	keyAccounts = "1"
	keyClean    = "2"
	keyPortsNet = "3"
	keyApps     = "4"
	keyShare    = "5"
	keySettings = "6"
)

// The digits that open an entry inside the two parent sections: Ports &
// Network (1 Fix stuck ports & apps, 2 Network tools) and Install & Update
// (1 Install developer apps, 2 Update everything).
const (
	keyPorts   = "1"
	keyNetwork = "2"
	keyInstall = "1"
	keyUpdate  = "2"
)

// The lead-in lines the two parent menus draw, so a scene can wait for the
// menu before it presses the digit that opens a screen behind it.
const (
	portsNetLead = "Free a busy port, check your connection."
	appsLead     = "Install dev apps, update everything."
)
