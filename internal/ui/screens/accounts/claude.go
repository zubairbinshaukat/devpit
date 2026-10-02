package accounts

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/claudeshare"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/itemlist"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The Claude setup brings the default account's skills, agents, commands,
// CLAUDE.md and settings to another Claude Code account. It asks one
// question with four answers (keep in sync, copy once, start empty,
// choose…); "choose…" opens the item list, where the Share/Copy
// explanation is always on screen, the login and account rows are shown
// locked, and a Careful row (MCP servers, which may hold API keys) is asked
// about twice before anything that may carry a secret is copied. The plan
// is previewed in the engine's words, including what it adds to the
// default account's folder, and applied with live rows. Later, the same
// screen shows where each item stands and offers stop sharing, share
// instead and repair.

// The explanation under the question and the item list.
var claudeLegend = []string{
	"Share = one folder for both, add once and both see it.",
	"Copy = a separate copy now; later changes stay apart.",
}

// claudeStage is where the Claude setup is.
type claudeStage int

const (
	clLoading claudeStage = iota
	clNoAccount
	clAsk
	clChoose
	clCareful
	clConflict
	clPlanning
	clChange
	clLinkFallback
	clStatus
	clFailed
)

// Messages of the Claude setup.
type (
	inventoryMsg struct {
		inv *claudeshare.Inventory
		err error
	}
	claudePlanMsg struct {
		plan claudeshare.Plan
		err  error
	}
)

// Answers to the question.
const (
	ansNotNow = "not-now"
)

// claudeScreen is the Claude setup for one account.
type claudeScreen struct {
	sess     *session
	name     string
	depth    int
	fromFlow bool
	run      *runHandle
	ctx      context.Context

	stage    claudeStage
	err      error
	inv      *claudeshare.Inventory
	ask      menu.Model
	items    itemlist.Model
	sel      claudeshare.Selection
	careful  confirm.Model
	asked    int
	pending  itemlist.WantsCarefulMsg
	conflict menu.Model
	plan     claudeshare.Plan
	change   change
	status   menu.Model
	statuses []claudeshare.ItemStatus
	keys     struct{ Back, Stop, Share, Repair, More, Copy, Retry key.Binding }
}

// newClaudeScreen returns the setup for the Claude Code account name; ""
// picks the account active in the folder, or the first one Devpit has.
// fromFlow says the change flow opened it right after adding the account:
// it then hands back to the flow when it closes.
func newClaudeScreen(s *session, name string, depth int, fromFlow bool) claudeScreen {
	h, ctx := newRunHandle()
	m := claudeScreen{sess: s, name: name, depth: depth, fromFlow: fromFlow, run: h, ctx: ctx}
	if m.name == "" {
		if ts, ok := s.status(accounts.ToolClaude); ok && !ts.Account.IsDefault() {
			m.name = ts.Account.Name
		}
	}
	m.keys.Back = bind("back", "enter")
	m.keys.Stop = bind("stop sharing", "s")
	m.keys.Share = bind("share instead", "h")
	m.keys.Repair = bind("repair", "r")
	m.keys.More = bind("bring more over", "b")
	m.keys.Copy = bind("copy instead", "c")
	m.keys.Retry = bind("try again", "r")
	return m
}

func (m claudeScreen) Init() tea.Cmd { return m.loadCmd() }

// loadCmd reads the item list. It changes nothing.
func (m claudeScreen) loadCmd() tea.Cmd {
	svc, name := m.sess.svc, m.name
	return func() tea.Msg {
		if name == "" {
			st, _, err := svc.Load()
			if err != nil {
				return inventoryMsg{err: err}
			}
			list := st.AccountsFor(accounts.ToolClaude)
			if len(list) == 0 {
				return inventoryMsg{}
			}
			name = list[0].Name
		}
		inv, err := svc.ClaudeInventory(name)
		return inventoryMsg{inv: inv, err: err}
	}
}

func (m claudeScreen) Title() string { return "Claude Code › Setup" }

func (m claudeScreen) Busy() bool { return m.stage == clChange && m.change.busy() }

func (m claudeScreen) Stop() { m.run.stop() }

func (m claudeScreen) TerminalProgress() *tea.ProgressBar { return m.change.progress() }

func (m claudeScreen) ShortHelp() []key.Binding {
	switch m.stage {
	case clAsk:
		return []key.Binding{m.ask.Keys.Up, m.ask.Keys.Select}
	case clChoose:
		return m.items.ShortHelp()
	case clCareful:
		return m.careful.Keys.ShortHelp()
	case clConflict:
		return []key.Binding{m.conflict.Keys.Up, m.conflict.Keys.Select}
	case clChange:
		return m.change.help()
	case clLinkFallback:
		return []key.Binding{m.keys.Copy, m.keys.Back}
	case clStatus:
		var out []key.Binding
		if st, ok := m.selectedStatus(); ok {
			if st.CanStopSharing {
				out = append(out, m.keys.Stop)
			}
			if st.CanShare {
				out = append(out, m.keys.Share)
			}
			if st.CanRepair {
				out = append(out, m.keys.Repair)
			}
		}
		return append(out, m.keys.More)
	case clFailed, clNoAccount:
		return []key.Binding{m.keys.Back}
	}
	return nil
}

func (m claudeScreen) FullHelp() [][]key.Binding {
	if m.stage == clChoose {
		return m.items.FullHelp()
	}
	return [][]key.Binding{m.ShortHelp()}
}

// leave closes the screen: back to the flow that opened it, or back.
func (m claudeScreen) leave() tea.Cmd {
	if m.fromFlow {
		return then(1, setupDoneMsg{})
	}
	return uictx.Pop()
}

func (m claudeScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		return m, nil
	}
	switch msg := msg.(type) {
	case inventoryMsg:
		return m.onInventory(msg)
	case claudePlanMsg:
		return m.onPlan(msg)
	case itemlist.WantsCarefulMsg:
		m.pending, m.asked = msg, 1
		m.stage = clCareful
		m.careful = confirm.New("careful", "Copy "+m.rowTitle(msg.ID)+"?",
			"They may hold API keys or other secrets"+m.secretNames(msg.ID)+". Only copy them if this account should have them.")
		return m, nil
	case confirm.AnsweredMsg:
		if m.stage != clCareful {
			break
		}
		if msg.Answer != confirm.AnswerYes {
			m.stage = clChoose
			return m, nil
		}
		if m.asked == 1 {
			m.asked = 2
			m.careful = confirm.New("careful", "Are you sure? Secrets copied now stay in "+m.target()+".",
				"Asking twice because this can copy API keys. Undo takes the copy back out.")
			return m, nil
		}
		m.sel.AllowSecrets = true
		next, cmd := m.items.Confirm(m.pending.Index, m.pending.Mode)
		m.items = next
		m.stage = clChoose
		return m, cmd
	case menu.SelectedMsg:
		return m.onSelect(msg)
	case itemlist.ProceedMsg:
		return m.proceed(m.selectionFromList())
	}
	return m.onOther(msg, ctx)
}

func (m claudeScreen) onInventory(msg inventoryMsg) (uictx.Screen, tea.Cmd) {
	switch {
	case msg.err != nil:
		m.stage, m.err = clFailed, msg.err
		return m, nil
	case msg.inv == nil:
		m.stage = clNoAccount
		return m, nil
	}
	m.inv = msg.inv
	m.name = msg.inv.Roots.TargetName
	m.statuses = m.inv.Status()
	if !m.fromFlow && sharesAnything(m.statuses) {
		m.stage = clStatus
		m.status = menu.New(m.statusItems())
		return m, nil
	}
	return m.toAsk()
}

// sharesAnything reports whether the account already shares or copied
// anything, so the screen starts on where things stand.
func sharesAnything(st []claudeshare.ItemStatus) bool {
	for _, s := range st {
		switch s.Status {
		case claudeshare.StatusShared, claudeshare.StatusBroken, claudeshare.StatusDrifted, claudeshare.StatusNotShared, claudeshare.StatusKeptLocal:
			return true
		}
	}
	return false
}

func (m claudeScreen) toAsk() (uictx.Screen, tea.Cmd) {
	items := make([]menu.Item, 0, 5)
	desc := map[claudeshare.Preset]string{
		claudeshare.PresetSync:     "Share what is safe to share; copy settings and hooks",
		claudeshare.PresetCopyOnce: "A separate copy of everything safe, now",
		claudeshare.PresetEmpty:    "Bring nothing over",
		claudeshare.PresetChoose:   "Pick item by item on the next screen",
	}
	for _, p := range claudeshare.Presets() {
		// A row that opens another screen says so in its description, not
		// with a trailing "…".
		items = append(items, menu.Item{ID: string(p), Title: strings.TrimSuffix(p.Title(), "…"), Desc: desc[p]})
	}
	if m.fromFlow {
		items = append(items, menu.Item{ID: ansNotNow, Title: "Not now", Desc: "Do this later from the Claude Code page"})
	}
	m.ask = menu.New(items)
	m.stage = clAsk
	return m, nil
}

// onSelect handles the question, the conflict question and the status
// list.
func (m claudeScreen) onSelect(msg menu.SelectedMsg) (uictx.Screen, tea.Cmd) {
	switch m.stage {
	case clAsk:
		switch msg.ID {
		case ansNotNow:
			return m, m.leave()
		case string(claudeshare.PresetChoose):
			m.items = itemlist.New(m.listRows(m.inv.Selection(claudeshare.PresetChoose)))
			m.sel = claudeshare.Selection{}
			m.stage = clChoose
			return m, nil
		case string(claudeshare.PresetEmpty):
			return m, m.leave()
		}
		return m.proceed(m.inv.Selection(claudeshare.Preset(msg.ID)))
	case clConflict:
		p := claudeshare.Policy(msg.ID)
		for k, c := range m.sel.Choices {
			c.Policy = p
			m.sel.Choices[k] = c
		}
		return m.planNow()
	}
	return m, nil
}

// proceed asks the conflict question when an item already exists in the
// account, and plans otherwise.
func (m claudeScreen) proceed(sel claudeshare.Selection) (uictx.Screen, tea.Cmd) {
	sel.AllowSecrets = sel.AllowSecrets || m.sel.AllowSecrets
	m.sel = sel
	n := 0
	for _, it := range m.inv.Items {
		if c, ok := sel.Choices[it.Kind]; ok && c.Mode != claudeshare.ModeSkip && it.Conflicts > 0 {
			n += it.Conflicts
		}
	}
	if n == 0 {
		return m.planNow()
	}
	m.stage = clConflict
	t := m.target()
	m.conflict = menu.New([]menu.Item{
		{ID: string(claudeshare.PolicyKeep), Title: "Keep", Desc: t + "'s own stay beside, renamed name.from-" + t},
		{ID: string(claudeshare.PolicyReplace), Title: "Replace", Desc: t + "'s own move to a dated backup folder inside " + t},
		{ID: string(claudeshare.PolicyKeepBoth), Title: "Keep both", Desc: t + "'s own are renamed name-" + t + " and shared too"},
	})
	return m, nil
}

// planNow builds the plan off the update loop.
func (m claudeScreen) planNow() (uictx.Screen, tea.Cmd) {
	m.stage = clPlanning
	svc, inv, sel := m.sess.svc, m.inv, m.sel
	return m, func() tea.Msg {
		p, err := svc.PlanClaudeSetup(inv, sel)
		return claudePlanMsg{plan: p, err: err}
	}
}

func (m claudeScreen) onPlan(msg claudePlanMsg) (uictx.Screen, tea.Cmd) {
	if msg.err != nil {
		m.stage, m.err = clFailed, msg.err
		return m, nil
	}
	m.plan = msg.plan
	svc, plan := m.sess.svc, msg.plan
	doc := previewDoc{sentences: plan.Preview(), noChange: plan.Empty()}
	m.change = newChange(m.ctx, m.sess, doc, "Make this change?", "One undo takes all of it back.", plan.Summary,
		func(ctx context.Context) <-chan accounts.Event { return svc.ApplyClaudePlan(ctx, plan) })
	m.change.fresh = accounts.ToolClaude
	m.stage = clChange
	return m, nil
}

// onOther routes keys and the mouse by stage.
func (m claudeScreen) onOther(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch m.stage {
	case clAsk:
		if next, cmd, ok := m.askMenu(ctx).Pointer(ctx, msg, m.askTop(ctx)); ok {
			m.ask = next
			return m, cmd
		}
		next, cmd := m.ask.Update(msg)
		m.ask = next
		return m, cmd
	case clChoose:
		if next, cmd, ok := m.items.SetSize(ctx.Width, max(4, ctx.BodyHeight-7)).Pointer(ctx, msg, 2); ok {
			m.items = next
			return m, cmd
		}
		next, cmd := m.items.Update(msg)
		m.items = next
		return m, cmd
	case clCareful:
		if km, ok := msg.(tea.KeyPressMsg); ok {
			next, cmd := m.careful.Update(km)
			m.careful = next
			return m, cmd
		}
		if cm, ok := msg.(tea.MouseClickMsg); ok && cm.Button == tea.MouseLeft {
			next, cmd := m.careful.Click(ctx, cm.X, ctx.BodyRow(cm.Y)-2)
			m.careful = next
			return m, cmd
		}
	case clConflict:
		if next, cmd, ok := m.conflict.Pointer(ctx, msg, 4); ok {
			m.conflict = next
			return m, cmd
		}
		next, cmd := m.conflict.Update(msg)
		m.conflict = next
		return m, cmd
	case clChange:
		next, cmd, out := m.change.update(msg, ctx, 2)
		m.change = next
		if m.change.stage == chFailed {
			if isLinkRefused(m.change.err) {
				m.stage = clLinkFallback
				return m, cmd
			}
		}
		switch out {
		case outcomeDeclined:
			return m.toAsk()
		case outcomeBack:
			if m.change.stage == chFailed && isInUse(m.change.err) {
				return m.planNow()
			}
			return m, m.leave()
		}
		return m, cmd
	case clLinkFallback:
		if km, ok := msg.(tea.KeyPressMsg); ok {
			switch {
			case key.Matches(km, m.keys.Copy):
				m.sel.CopyInsteadOfLinks = true
				return m.planNow()
			case key.Matches(km, m.keys.Back):
				return m, m.leave()
			}
		}
	case clStatus:
		return m.onStatusKey(msg, ctx)
	case clFailed, clNoAccount:
		if km, ok := msg.(tea.KeyPressMsg); ok && key.Matches(km, m.keys.Back) {
			return m, m.leave()
		}
	}
	return m, nil
}

// --- later changes ---

func (m claudeScreen) statusItems() []menu.Item {
	out := make([]menu.Item, 0, len(m.statuses))
	for i, s := range m.statuses {
		title := s.Kind.Title()
		if s.Name != "" && s.Name != string(s.Kind) {
			title += ": " + s.Name
		}
		out = append(out, menu.Item{ID: fmt.Sprint(i), Title: title, Hint: statusWord(s.Status), Desc: s.Detail})
	}
	return out
}

// statusWord is a status in the list's words.
func statusWord(s claudeshare.Status) string {
	switch s {
	case claudeshare.StatusShared:
		return "shared"
	case claudeshare.StatusCopy:
		return "copy, same"
	case claudeshare.StatusDrifted:
		return "copy, changed since"
	case claudeshare.StatusOwn:
		return "its own"
	case claudeshare.StatusBroken:
		return "broken link"
	case claudeshare.StatusNotShared:
		return "not shared yet"
	case claudeshare.StatusKeptLocal:
		return "kept its own"
	case claudeshare.StatusForeign:
		return "link made elsewhere"
	case claudeshare.StatusMissing:
		return "only in default"
	}
	return string(s)
}

func (m claudeScreen) selectedStatus() (claudeshare.ItemStatus, bool) {
	it, ok := m.status.Selected()
	if !ok {
		return claudeshare.ItemStatus{}, false
	}
	var i int
	_, _ = fmt.Sscan(it.ID, &i)
	if i < 0 || i >= len(m.statuses) {
		return claudeshare.ItemStatus{}, false
	}
	return m.statuses[i], true
}

func (m claudeScreen) onStatusKey(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		if next, cmd, ok := m.statusMenu(ctx).Pointer(ctx, msg, m.statusTop(ctx)); ok {
			m.status = next
			return m, cmd
		}
		next, cmd := m.status.Update(msg)
		m.status = next
		return m, cmd
	}
	st, have := m.selectedStatus()
	var op service.ClaudeOp
	switch {
	case key.Matches(km, m.keys.More):
		return m.toAsk()
	case have && st.CanStopSharing && key.Matches(km, m.keys.Stop):
		op = service.ClaudeStopSharing
	case have && st.CanShare && key.Matches(km, m.keys.Share):
		op = service.ClaudeShareInstead
	case have && st.CanRepair && key.Matches(km, m.keys.Repair):
		op = service.ClaudeRepair
	default:
		next, cmd := m.status.Update(km)
		m.status = next
		return m, cmd
	}
	m.stage = clPlanning
	svc, name := m.sess.svc, m.name
	c := service.ClaudeChange{Op: op, Kind: st.Kind, Names: []string{st.Name}}
	return m, func() tea.Msg {
		p, err := svc.PlanClaudeChange(name, c)
		return claudePlanMsg{plan: p, err: err}
	}
}

// --- the item list ---

// listRows turns the inventory into the item list, starting from sel.
func (m claudeScreen) listRows(sel claudeshare.Selection) []itemlist.Row {
	out := make([]itemlist.Row, 0, len(m.inv.Items))
	for _, it := range m.inv.Items {
		r := itemlist.Row{ID: string(it.Kind), Title: it.Title, Detail: itemDetail(it), Label: listLabel(it.Label), Hint: it.Note}
		if it.Locked {
			r.Locked, r.LockedReason = true, "never cloned"
			out = append(out, r)
			continue
		}
		for _, md := range it.Modes {
			r.Modes = append(r.Modes, listMode(md))
		}
		if !it.Offers(claudeshare.ModeSkip) {
			r.Modes = append(r.Modes, itemlist.ModeSkip)
		}
		r.Default = listMode(it.Default)
		r.Mode = listMode(sel.Choices[it.Kind].Mode)
		r.NeedsConfirm = it.Label == claudeshare.LabelCareful || it.Asks >= 2
		if it.Conflicts > 0 {
			r.Note = plural(it.Conflicts, "1 has the same name in "+m.target(), "%d have the same name in "+m.target())
		}
		if it.Source.Size > 0 {
			r.Bytes = uint64(it.Source.Size)
		}
		out = append(out, r)
	}
	return out
}

// selectionFromList is the item list as picked.
func (m claudeScreen) selectionFromList() claudeshare.Selection {
	sel := claudeshare.Selection{Choices: map[claudeshare.Kind]claudeshare.Choice{}, AllowSecrets: m.sel.AllowSecrets}
	for _, r := range m.items.Rows() {
		if r.Locked {
			continue
		}
		sel.Choices[claudeshare.Kind(r.ID)] = claudeshare.Choice{Mode: shareMode(r.Mode), Policy: claudeshare.PolicyKeep}
	}
	return sel
}

// itemDetail is a row's size: "12 skills · 340 KB".
func itemDetail(it claudeshare.Item) string {
	var parts []string
	nouns := map[claudeshare.Kind][2]string{
		claudeshare.KindSkills: {"skill", "skills"}, claudeshare.KindAgents: {"agent", "agents"},
		claudeshare.KindCommands: {"command", "commands"}, claudeshare.KindPlugins: {"enabled", "enabled"},
		claudeshare.KindHooks: {"hook", "hooks"}, claudeshare.KindMCP: {"server", "servers"},
	}
	if n := it.Source.Count; n > 0 {
		if w, ok := nouns[it.Kind]; ok {
			parts = append(parts, plural(n, "1 "+w[0], "%d "+w[1]))
		}
	}
	if it.Source.Size > 0 {
		parts = append(parts, header.FormatBytes(uint64(it.Source.Size)))
	}
	return strings.Join(parts, ", ")
}

func listLabel(l claudeshare.Label) itemlist.Label {
	switch l {
	case claudeshare.LabelReview:
		return itemlist.LabelReview
	case claudeshare.LabelCareful:
		return itemlist.LabelCareful
	}
	return itemlist.LabelSafe
}

func listMode(md claudeshare.Mode) itemlist.Mode {
	switch md {
	case claudeshare.ModeShare:
		return itemlist.ModeShare
	case claudeshare.ModeSameList:
		return itemlist.ModeSameList
	case claudeshare.ModeCopy:
		return itemlist.ModeCopy
	}
	return itemlist.ModeSkip
}

func shareMode(md itemlist.Mode) claudeshare.Mode {
	switch md {
	case itemlist.ModeShare:
		return claudeshare.ModeShare
	case itemlist.ModeSameList:
		return claudeshare.ModeSameList
	case itemlist.ModeCopy:
		return claudeshare.ModeCopy
	}
	return claudeshare.ModeSkip
}

// rowTitle is an item's name.
func (m claudeScreen) rowTitle(id string) string {
	if it, ok := m.inv.Item(claudeshare.Kind(id)); ok {
		return it.Title
	}
	return id
}

// secretNames lists the names of what may carry a secret (names only).
func (m claudeScreen) secretNames(id string) string {
	it, ok := m.inv.Item(claudeshare.Kind(id))
	if !ok || len(it.Secrets) == 0 {
		return ""
	}
	return " (" + strings.Join(it.Secrets, ", ") + ")"
}

// target is the account's name.
func (m claudeScreen) target() string {
	if m.name != "" {
		return m.name
	}
	return "this account"
}

// --- views ---

func (m claudeScreen) askTop(ctx uictx.Context) int { return 2 + len(m.askIntro(ctx)) + 1 }

// askMenu is the question's answers at the height View draws them.
func (m claudeScreen) askMenu(ctx uictx.Context) menu.Model {
	return m.ask.SetHeight(max(3, ctx.BodyHeight-m.askTop(ctx)-4))
}

// statusMenu is the status list at the height View draws it.
func (m claudeScreen) statusMenu(ctx uictx.Context) menu.Model {
	return m.status.SetHeight(max(3, ctx.BodyHeight-m.statusTop(ctx)))
}

// statusTop is the body row the status list starts on.
func (m claudeScreen) statusTop(ctx uictx.Context) int {
	if m.inv != nil && m.inv.NotSharedYet() != "" {
		return 4
	}
	return 2
}

func (m claudeScreen) askIntro(ctx uictx.Context) []string {
	return wrap(ctx, ctx.Theme.Base, m.target()+" can have your default account's skills, agents, commands, CLAUDE.md and settings. The login itself is never copied.", 1, 0)
}

func (m claudeScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	title := "Bring your Claude Code setup to " + m.target() + "?"
	switch m.stage {
	case clStatus:
		title = "What " + m.target() + " shares"
	case clChoose, clCareful:
		title = "Choose what " + m.target() + " gets"
	case clConflict:
		title = "Some items are already in " + m.target()
	case clChange:
		title = "Bring the setup to " + m.target()
	}
	out := []string{heading(ctx, title, ""), ""}
	switch m.stage {
	case clLoading, clPlanning:
		out = append(out, " "+th.Muted.Render("Looking at both setups"+ellipsis(ctx)))
	case clNoAccount:
		out = append(out, wrap(ctx, th.Base, "There is no second Claude Code account to set up yet. Add one first: Claude Code › Sign in with another account.", 1, 0)...)
	case clAsk:
		out = append(out, m.askIntro(ctx)...)
		out = append(out, "", m.askMenu(ctx).View(ctx), "")
		out = append(out, itemlist.Legend(ctx, ctx.Width, claudeLegend...))
	case clChoose:
		list := m.items.SetSize(ctx.Width, max(4, ctx.BodyHeight-7))
		out = append(out, list.View(ctx), "")
		out = append(out, itemlist.Legend(ctx, ctx.Width, claudeLegend...))
		out = append(out, "", "    "+itemlist.SummaryLine(ctx, m.items.Summary()))
	case clCareful:
		out = append(out, m.careful.View(ctx))
	case clConflict:
		out = append(out, wrap(ctx, th.Base, "Nothing is deleted or overwritten whichever you pick.", 1, 0)...)
		out = append(out, "", m.conflict.View(ctx))
	case clChange:
		out = append(out, m.change.view(ctx, ctx.BodyHeight-2)...)
	case clLinkFallback:
		out = append(out, strings.Split(errorView(ctx, m.change.err), "\n")...)
		out = append(out, "", keyHints(ctx, "c", "copy instead (you see the new preview first)", "enter", "back"))
	case clStatus:
		if inv := m.inv; inv != nil {
			if n := inv.NotSharedYet(); n != "" {
				out = append(out, noticeLine(ctx, notice{"warning", n}), "")
			}
		}
		out = append(out, m.statusMenu(ctx).View(ctx))
	case clFailed:
		out = append(out, errorView(ctx, m.err), "", keyHints(ctx, "enter", "back"))
	}
	return strings.Join(out, "\n")
}
