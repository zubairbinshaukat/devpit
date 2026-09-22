package ports

import "charm.land/bubbles/v2/key"

// ShortHelp implements uictx.Screen. The hints shown depend on the stage, so
// the footer never shows a key that does nothing right now.
func (m Model) ShortHelp() []key.Binding {
	switch m.stage {
	case stageKillInput:
		binds := []key.Binding{m.keys.Select, m.keys.Backspace}
		return binds
	case stageKillConfirm:
		binds := []key.Binding{m.killConfirm.Keys.Yes, m.killConfirm.Keys.Confirm}
		if m.killOfferTree {
			binds = append(binds, m.keys.ToggleTree)
		}
		return binds
	case stageList:
		return []key.Binding{m.keys.Up, m.keys.Down, m.keys.Space, m.keys.Kill, m.keys.Refresh}
	case stageListConfirm:
		binds := []key.Binding{m.listConfirm.Keys.Yes, m.listConfirm.Keys.Confirm}
		if m.listOfferTree {
			binds = append(binds, m.keys.ToggleTree)
		}
		return binds
	case stageSummary:
		return []key.Binding{m.summaryModel.Keys.Continue}
	default:
		return []key.Binding{m.submenu.Keys.Up, m.submenu.Keys.Select}
	}
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding {
	switch m.stage {
	case stageKillInput:
		return [][]key.Binding{{m.keys.Select, m.keys.Backspace}}
	case stageKillConfirm:
		return [][]key.Binding{{m.killConfirm.Keys.Yes, m.killConfirm.Keys.Confirm, m.keys.ToggleTree}}
	case stageList:
		return [][]key.Binding{
			{m.keys.Up, m.keys.Down, m.keys.Space},
			{m.keys.Kill, m.keys.Refresh},
		}
	case stageListConfirm:
		return [][]key.Binding{{m.listConfirm.Keys.Yes, m.listConfirm.Keys.Confirm, m.keys.ToggleTree}}
	case stageSummary:
		return [][]key.Binding{{m.summaryModel.Keys.Continue}}
	default:
		return [][]key.Binding{{m.submenu.Keys.Up, m.submenu.Keys.Down, m.submenu.Keys.Select}}
	}
}
