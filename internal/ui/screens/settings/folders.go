package settings

import (
	"os"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// folderRowDefault is the ID of the first row of a folderScreen's list: the
// default projects folder. Every row after it is a recent folder.
const folderRowDefault = "default"

// folderScreen edits the default projects folder and lets the user remove
// entries from the recent-folders list. It never adds to the recent list
// itself; that happens when a scan actually runs (a later milestone), via
// config.Config.AddRecentFolder.
type folderScreen struct {
	list    menu.Model
	input   textinput.Model
	editing bool
	errMsg  string

	edit   key.Binding
	remove key.Binding
	back   key.Binding
}

// newFolderScreen returns the folders sub-screen seeded from cfg.
func newFolderScreen(cfg config.Config) folderScreen {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.Placeholder = `C:\Users\you\code`
	return folderScreen{
		list:   menu.New(folderItems(cfg)),
		input:  ti,
		edit:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "edit/save")),
		remove: key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "remove")),
		back:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

// folderItems rebuilds the list rows from cfg: the default folder first,
// then every recent folder in order.
func folderItems(cfg config.Config) []menu.Item {
	def := cfg.DefaultProjectsFolder
	if def == "" {
		def = "(not set)"
	}
	items := make([]menu.Item, 0, len(cfg.RecentFolders)+1)
	items = append(items, menu.Item{ID: folderRowDefault, Title: "Default folder: " + def, Desc: "Enter to change"})
	for _, f := range cfg.RecentFolders {
		items = append(items, menu.Item{ID: "recent", Title: f, Desc: "x to remove"})
	}
	return items
}

// Init implements uictx.Screen.
func (s folderScreen) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (s folderScreen) Title() string { return "Projects folder" }

// ShortHelp implements uictx.Screen.
func (s folderScreen) ShortHelp() []key.Binding {
	return []key.Binding{s.list.Keys.Up, s.edit, s.remove, s.back}
}

// FullHelp implements uictx.Screen.
func (s folderScreen) FullHelp() [][]key.Binding {
	return [][]key.Binding{{s.list.Keys.Up, s.list.Keys.Down}, {s.edit, s.remove, s.back}}
}

// Update implements uictx.Screen.
func (s folderScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if s.editing {
		return s.updateEditing(msg, ctx)
	}

	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(km, s.edit):
			if it, ok := s.list.Selected(); ok && it.ID == folderRowDefault {
				s.editing = true
				s.errMsg = ""
				s.input.SetValue(ctx.Config.DefaultProjectsFolder)
				s.input.CursorEnd()
				return s, s.input.Focus()
			}
		case key.Matches(km, s.remove):
			if it, ok := s.list.Selected(); ok && it.ID == "recent" {
				idx := s.list.Cursor() - 1
				cfg := ctx.Config
				if idx >= 0 && idx < len(cfg.RecentFolders) {
					cfg.RecentFolders = slices.Delete(slices.Clone(cfg.RecentFolders), idx, idx+1)
					s.list = s.list.SetItems(folderItems(cfg))
					return s, uictx.SaveConfig(cfg)
				}
			}
		}
	}

	next, cmd := s.list.Update(msg)
	s.list = next
	return s, cmd
}

// updateEditing handles keys while the default-folder text field is
// focused.
func (s folderScreen) updateEditing(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok && key.Matches(km, s.edit) {
		path := strings.TrimSpace(s.input.Value())
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			s.errMsg = "That folder does not exist."
			return s, nil
		}
		cfg := ctx.Config
		cfg.DefaultProjectsFolder = path
		s.editing = false
		s.errMsg = ""
		s.input.Blur()
		s.list = s.list.SetItems(folderItems(cfg))
		return s, uictx.SaveConfig(cfg)
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return s, cmd
}

// View implements uictx.Screen.
func (s folderScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(th.Muted.Render(ctx.Wrap(
		"The default folder is what Free Up Disk Space scans first. Recent folders remember the last ten you scanned.")))
	b.WriteString("\n\n")

	if s.editing {
		b.WriteString(th.Base.Render("New default folder:"))
		b.WriteString("\n")
		b.WriteString(s.input.View())
	} else {
		b.WriteString(s.list.View(ctx))
	}

	if s.errMsg != "" {
		b.WriteString("\n\n")
		b.WriteString(th.Danger.Render(ctx.Icons.Warn + " " + s.errMsg))
	}
	return b.String()
}
