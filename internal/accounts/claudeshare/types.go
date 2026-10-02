package claudeshare

// Kind is one row of the item list.
type Kind string

// The rows, in the order the item list shows them.
const (
	KindSkills      Kind = "skills"
	KindAgents      Kind = "agents"
	KindCommands    Kind = "commands"
	KindClaudeMD    Kind = "claude-md"
	KindPlugins     Kind = "plugins"
	KindSettings    Kind = "settings"
	KindHooks       Kind = "hooks"
	KindMCP         Kind = "mcp"
	KindHistory     Kind = "history"
	KindLogin       Kind = "login"
	KindAccountInfo Kind = "account-info"
)

// Kinds lists every row in display order.
func Kinds() []Kind {
	return []Kind{
		KindSkills, KindAgents, KindCommands, KindClaudeMD, KindPlugins,
		KindSettings, KindHooks, KindMCP, KindHistory, KindLogin, KindAccountInfo,
	}
}

// Title is the row's name in the item list.
func (k Kind) Title() string {
	switch k {
	case KindSkills:
		return "Skills"
	case KindAgents:
		return "Agents"
	case KindCommands:
		return "Commands"
	case KindClaudeMD:
		return "CLAUDE.md"
	case KindPlugins:
		return "Plugins"
	case KindSettings:
		return "Settings"
	case KindHooks:
		return "Hooks"
	case KindMCP:
		return "MCP servers"
	case KindHistory:
		return "Chat history"
	case KindLogin:
		return "Login"
	case KindAccountInfo:
		return "Account info"
	}
	return string(k)
}

// Label is how careful the person should be with a row.
type Label int

// Labels, from harmless to never selectable.
const (
	LabelSafe Label = iota
	LabelReview
	LabelCareful
	LabelLocked
)

func (l Label) String() string {
	switch l {
	case LabelSafe:
		return "Safe"
	case LabelReview:
		return "Review"
	case LabelCareful:
		return "Careful"
	}
	return "Locked"
}

// Mode is what happens to a row.
type Mode string

// Modes. Not every row offers every mode; see [Item.Modes].
const (
	// ModeShare links the account to the default's copy (junction, or the
	// CLAUDE.md import line).
	ModeShare Mode = "share"
	// ModeSameList copies the enabled-plugin list and the marketplaces;
	// each account installs its own copy of the plugins.
	ModeSameList Mode = "same-list"
	// ModeCopy gives the account its own copy, once.
	ModeCopy Mode = "copy"
	// ModeSkip leaves the row alone.
	ModeSkip Mode = "skip"
	// ModeLocked is the login and account rows: never selectable.
	ModeLocked Mode = "locked"
)

// Title is the mode in the item list's words.
func (m Mode) Title() string {
	switch m {
	case ModeShare:
		return "Share"
	case ModeSameList:
		return "Same list"
	case ModeCopy:
		return "Copy"
	case ModeSkip:
		return "Skip"
	}
	return "Locked"
}

// Policy is what happens when the account already has an item of the same
// name. Nothing is deleted or overwritten under any policy.
type Policy string

// Policies.
const (
	// PolicyKeep (the default): with Share, the account's items whose
	// names are new become shared (moved into the default account and
	// linked back), and its same-named ones stay beside as
	// <name>.from-<account>. With Copy, the account's own item keeps its
	// name and the default's is put beside it as <name>.from-default. For
	// settings-style keys the account's own value stays.
	PolicyKeep Policy = "keep"
	// PolicyReplace moves the account's same-named items to a dated backup
	// folder inside the account; the shared (or copied) one takes the name.
	PolicyReplace Policy = "replace"
	// PolicyKeepBoth renames the account's same-named items <name>-<account>;
	// with Share they are shared too.
	PolicyKeepBoth Policy = "keep-both"
)

// Title is the policy in the conflict screen's words.
func (p Policy) Title() string {
	switch p {
	case PolicyReplace:
		return "Replace"
	case PolicyKeepBoth:
		return "Keep both"
	}
	return "Keep"
}

// EntryState is where one named entry of a row stands.
type EntryState string

// Entry states.
const (
	// EntrySame: both accounts have it, with the same bytes. Not a conflict.
	EntrySame EntryState = "same"
	// EntryDiffers: both have it, with different content: a conflict.
	EntryDiffers EntryState = "differs"
	// EntrySourceOnly: only the default account has it.
	EntrySourceOnly EntryState = "source-only"
	// EntryTargetOnly: only this account has it ("new in this account, not
	// shared yet" for a skill).
	EntryTargetOnly EntryState = "target-only"
	// EntryLinked: this account links to the default's copy.
	EntryLinked EntryState = "linked"
	// EntryBroken: a link whose target is gone; it can be repaired.
	EntryBroken EntryState = "broken"
	// EntryForeignLink: a link or symbolic link made by something else.
	// Devpit leaves it alone and says so.
	EntryForeignLink EntryState = "foreign-link"
	// EntryKeptLocal: this account keeps its own on purpose (a stopped
	// share, a Keep rename, or a Replace that left it local).
	EntryKeptLocal EntryState = "kept-local"
)

// Entry is one named thing inside a row: a skill, an agent file, a plugin, a
// settings key, an MCP server.
type Entry struct {
	Name  string
	State EntryState
	// Section groups entries of one row: "enabledPlugins" or
	// "extraKnownMarketplaces" for plugins, "" elsewhere.
	Section string
	// SourcePath and TargetPath are set for file-system entries.
	SourcePath, TargetPath string
	// Size is the bytes of the default's copy (or the account's, when only
	// it has one). Links count zero.
	Size int64
	// LinkTarget is where a link points.
	LinkTarget string
	// Secret is set when the entry may carry a secret: an MCP server with
	// env or headers values, or a settings key like env or apiKeyHelper.
	// Only the name is ever shown.
	Secret bool
}

// Side is what one account has for a row.
type Side struct {
	Exists bool
	Path   string
	Count  int
	// Size is the bytes held in this account, never counting through a
	// link.
	Size int64
	// Links is how many entries are links.
	Links int
}

// Item is one row of the item list.
type Item struct {
	Kind    Kind
	Title   string
	Label   Label
	Default Mode
	// Modes are the modes the row offers.
	Modes []Mode
	// Locked rows are always shown and never selectable.
	Locked bool
	// Note is a plain sentence about the row.
	Note string
	// Source is the default account, Target the account being set up.
	Source, Target Side
	Entries        []Entry
	// Conflicts counts entries that differ.
	Conflicts int
	// Secrets names the keys or servers that may carry secrets (names
	// only, never values).
	Secrets []string
	// LinkOK says whether a link can be made for this row; LinkWhy is the
	// reason when it cannot, and Share falls back to Copy.
	LinkOK  bool
	LinkWhy string
	// Shared is set when the row is shared now.
	Shared bool
	// Asks is how many times the screen asks before applying: 2 for
	// Careful rows.
	Asks int
}

// Offers reports whether the row offers mode m.
func (it Item) Offers(m Mode) bool {
	for _, x := range it.Modes {
		if x == m {
			return true
		}
	}
	return false
}

// Inventory is the item list for one source and one target account.
type Inventory struct {
	Roots Roots
	Items []Item
	// Warnings are sentences about things Devpit left alone.
	Warnings []string

	state State
}

// Item returns the row of kind k.
func (inv *Inventory) Item(k Kind) (Item, bool) {
	for _, it := range inv.Items {
		if it.Kind == k {
			return it, true
		}
	}
	return Item{}, false
}

// Choice is what the person picked for one row.
type Choice struct {
	Mode   Mode
	Policy Policy
}

// Selection is the whole item list as picked.
type Selection struct {
	Choices map[Kind]Choice
	// Names limits a row to these entries (case-insensitive). Empty means
	// every entry.
	Names map[Kind][]string
	// AllowSecrets is set only after the Careful path was confirmed twice:
	// then the settings keys that may hold secrets (env, apiKeyHelper...)
	// are copied too. MCP servers need it as well.
	AllowSecrets bool
	// CopyInsteadOfLinks plans copies where links were asked for. Set it
	// after Apply returned a [*LinkError], and show the new preview.
	CopyInsteadOfLinks bool
}

// Preset is one of the four quick answers to "Bring your setup over?".
type Preset string

// The four quick answers.
const (
	// PresetSync is "Keep them in sync": every row's default.
	PresetSync Preset = "sync"
	// PresetCopyOnce is "Copy them once": Share rows become Copy.
	PresetCopyOnce Preset = "copy-once"
	// PresetEmpty is "Start empty": nothing is brought over.
	PresetEmpty Preset = "empty"
	// PresetChoose is "Choose…": the defaults, for the person to change.
	PresetChoose Preset = "choose"
)

// Presets lists the quick answers in display order.
func Presets() []Preset { return []Preset{PresetSync, PresetCopyOnce, PresetEmpty, PresetChoose} }

// Title is the quick answer's words.
func (p Preset) Title() string {
	switch p {
	case PresetSync:
		return "Keep them in sync"
	case PresetCopyOnce:
		return "Copy them once"
	case PresetEmpty:
		return "Start empty"
	}
	return "Choose…"
}

// Selection returns the selection a quick answer stands for. Careful rows
// are never ticked by a preset; Locked rows never appear.
func (inv *Inventory) Selection(p Preset) Selection {
	sel := Selection{Choices: map[Kind]Choice{}}
	for _, it := range inv.Items {
		if it.Locked {
			continue
		}
		m := it.Default
		switch p {
		case PresetEmpty:
			m = ModeSkip
		case PresetCopyOnce:
			if m == ModeShare {
				m = ModeCopy
			}
		}
		if it.Label == LabelCareful {
			m = ModeSkip
		}
		if !it.Offers(m) {
			m = ModeSkip
		}
		sel.Choices[it.Kind] = Choice{Mode: m, Policy: PolicyKeep}
	}
	return sel
}
