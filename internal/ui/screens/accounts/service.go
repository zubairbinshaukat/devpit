package accounts

import (
	"context"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/claudeshare"
	"github.com/zubairbinshaukat/devpit/internal/accounts/importer"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/gitssh"
)

// Service is the Accounts engine as these screens use it. The real one is
// *service.Service; tests and the screenshot renderer pass a fake. Every
// sentence a screen shows about a change comes from here (the engine's
// Preview), never from the screens, so the command line and the menu say
// exactly the same thing.
type Service interface {
	// The page.
	Overview(ctx context.Context, folder string) (service.Overview, error)
	Status(ctx context.Context, tool accounts.Tool, folder string) (service.ToolStatus, error)
	List(ctx context.Context, tool accounts.Tool, folder string) (service.ListJSON, error)
	Caps(ctx context.Context, t accounts.Tool) adapters.Caps
	LiveCheckRisk(t accounts.Tool) (bool, string)
	Load() (*accounts.Store, string, error)
	FolderCheck(folder string) string
	SyncShims(emit func(accounts.Event))
	RecoveryReport() ([]accounts.Recovered, error)
	Hold() error
	Release()
	StaleRules() ([]accounts.StaleRule, error)
	DisplayPath(p string) string

	// Changes.
	Plan(ctx context.Context, c accounts.Change) (accounts.Preview, error)
	Apply(ctx context.Context, p accounts.Preview) <-chan accounts.Event
	ApplyGroup(ctx context.Context, previews []accounts.Preview) <-chan accounts.Event
	Verify(ctx context.Context, opt service.VerifyOptions) (service.VerifyReport, error)
	UndoPreview() (service.UndoInfo, error)
	Undo(ctx context.Context, emit func(accounts.Event)) (accounts.UndoResult, error)

	// Accounts.
	SignIn(ctx context.Context, t accounts.Tool, req adapters.LoginRequest) (<-chan accounts.Event, error)
	PrepareSignInAgain(ctx context.Context, t accounts.Tool, name string) (service.OnceCommand, error)
	DiscardSignIn(acct accounts.Account) (string, error)
	SuggestName(t accounts.Tool, email string) string
	CheckNewName(t accounts.Tool, name string) error
	SaveAccount(acct accounts.Account, name string) (accounts.Account, accounts.Entry, error)
	PlanAccountEdit(ctx context.Context, e service.AccountEdit) (service.AccountEditPreview, error)
	ApplyAccountEdit(ctx context.Context, p service.AccountEditPreview) <-chan accounts.Event

	// The Git page.
	CommitsAs(ctx context.Context, folder string) (adapters.GitCommitIdentity, error)
	PushesAs(ctx context.Context, folder string) (adapters.GitHubPush, error)
	SuggestEmails(ctx context.Context) ([]adapters.EmailSuggestion, error)
	SuggestedSSHKeyPath(name string) string
	GenerateSSHKey(ctx context.Context, path, comment string) (gitssh.KeygenResult, error)
	PlanSSHKey(folder, keyPath string) (adapters.GitSettingsPreview, error)
	ApplyGitSettings(ctx context.Context, p adapters.GitSettingsPreview) <-chan accounts.Event

	// Claude Code setup.
	ClaudeInventory(name string) (*claudeshare.Inventory, error)
	PlanClaudeSetup(inv *claudeshare.Inventory, sel claudeshare.Selection) (claudeshare.Plan, error)
	PlanClaudeChange(name string, c service.ClaudeChange) (claudeshare.Plan, error)
	ApplyClaudePlan(ctx context.Context, p claudeshare.Plan) <-chan accounts.Event

	// Accounts already on this PC.
	DetectClaudeAcc(ctx context.Context) (importer.Found, bool, error)
	PlanImport(f importer.Found, opt importer.Options) (importer.Plan, error)
	ApplyImport(p importer.Plan, emit func(accounts.Event)) (accounts.Entry, error)
	ImportDismissed(f importer.Found) bool
	DismissImport(f importer.Found) error
}

// The real service is one.
var _ Service = (*service.Service)(nil)

// Options build the Accounts screens. Every zero field is the real thing;
// tests and the screenshot renderer set their own.
type Options struct {
	// Folder is the folder the page opens on; the folder Devpit was started
	// in when "".
	Folder string
	// Open opens the engine. It runs in a command after the first frame:
	// opening finishes or rolls back a change a crash left half done.
	Open func() (Service, error)
	// Exec hands the terminal to a sign-in; tea.Exec when nil.
	Exec func(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd
	// Copy puts text on the clipboard and says so.
	Copy func(text string) tea.Cmd
	// Tick schedules the spinner's next frame; tea.Tick when nil. Tests pass
	// one that never fires.
	Tick func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd
	// Stat looks at a folder; os.Stat when nil.
	Stat func(string) (os.FileInfo, error)
	// Getwd is the folder Devpit was started in; os.Getwd when nil.
	Getwd func() (string, error)
	// Browse opens the Windows folder browser for "Another folder…";
	// the picker's own when nil.
	Browse func(start string) (string, error)
	// RunOnce runs a prepared command on the terminal (sign in again);
	// service.RunOnce when nil.
	RunOnce func(service.OnceCommand) (int, error)
}

// withDefaults fills the zero fields.
func (o Options) withDefaults() Options {
	if o.Open == nil {
		o.Open = func() (Service, error) { return service.Open(service.Options{}) }
	}
	if o.Exec == nil {
		o.Exec = tea.Exec
	}
	if o.Copy == nil {
		o.Copy = copyToClipboard
	}
	if o.Tick == nil {
		o.Tick = tea.Tick
	}
	if o.Stat == nil {
		o.Stat = os.Stat
	}
	if o.Getwd == nil {
		o.Getwd = os.Getwd
	}
	if o.RunOnce == nil {
		o.RunOnce = service.RunOnce
	}
	return o
}

// copyToClipboard tries the native Windows clipboard first and falls back to
// asking the terminal (OSC 52), the way Share Files and the SSH key screen
// do. Its result is a footer status, so the person sees it happened.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		if gitssh.CopyWin32(text) == nil {
			return statusOK("Copied to the clipboard")
		}
		return tea.BatchMsg{tea.SetClipboard(text), func() tea.Msg { return statusOK("Copied to the clipboard") }}
	}
}
