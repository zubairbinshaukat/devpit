// Package share is the Share Files section: a small menu that opens the
// sharing screen (make a folder available to another PC on the network) or
// the receiving screen (copy a shared folder from another PC), and offers to
// resume an interrupted copy or clean up an old share on the way in.
//
// The screens hold no engine logic. Everything that touches the machine goes
// through internal/share/host and internal/share/recv behind the small
// interfaces in this file, so every state of every screen renders in a test
// with no network, no Windows and no administrator prompt.
//
// Safety rules 18 and 23 to 27 (docs/safety.md) apply to what these screens
// start: the admin work runs in the elevated worker only, and Stop, quit and a
// crash all end with the share removed.
package share

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/gitssh"
	"github.com/zubairbinshaukat/devpit/internal/share/errmap"
	"github.com/zubairbinshaukat/devpit/internal/share/host"
	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/share/robocopy"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Hoster is the sharing engine as the screen uses it. *host.Manager is the
// real one; tests pass a fake.
type Hoster interface {
	// Adapters lists the network connections to offer.
	Adapters(ctx context.Context) ([]host.Adapter, error)
	// CategoryOf reads a connection's network category.
	CategoryOf(ctx context.Context, a host.Adapter) (host.Category, error)
	// Start sets the share up.
	Start(ctx context.Context, opts host.Options, onStep func(host.Step)) (host.Card, error)
	// Stop takes it all down.
	Stop(ctx context.Context) error
	// Active reports whether anything is set up.
	Active() bool
}

// Receiver is the receiving engine as the screen uses it. *recv.Session is
// the real one.
type Receiver interface {
	// List checks an address and lists its shares.
	List(ctx context.Context, typed string) (string, []netstat.Share, error)
	// SignInHost signs in to a PC and lists its shares.
	SignInHost(ctx context.Context, host string, creds netstat.Credentials) ([]netstat.Share, error)
	// SignInShare signs in to one share.
	SignInShare(host, share string, creds netstat.Credentials) error
	// DropConnections closes the open connections to a PC.
	DropConnections(ctx context.Context, host string) (int, error)
	// Preflight measures a copy and checks the destination.
	Preflight(ctx context.Context, host, share, dest string) (recv.Check, error)
	// Start makes the record of a copy.
	Start(host, share, user, dest string, plan robocopy.Plan) job.Job
	// Run copies.
	Run(ctx context.Context, j job.Job, onEvent func(recv.Event)) (recv.Outcome, error)
	// Disconnect closes a share's connection after a copy.
	Disconnect(host, share string)
	// ClearJob forgets the saved copy.
	ClearJob() error
}

// Deps is everything the screens reach outside themselves.
type Deps struct {
	// NewHost builds the sharing engine when the screen opens.
	NewHost func() Hoster
	// NewRecv builds the receiving engine when the screen opens.
	NewRecv func() Receiver
	// Leftover reports a share an earlier run left behind.
	Leftover func() (host.Manifest, bool)
	// CleanUp removes that share, with one admin prompt.
	CleanUp func(ctx context.Context, m host.Manifest) error
	// SavedJob returns an interrupted copy that can be resumed.
	SavedJob func() (job.Job, bool)
	// Copy puts text on the clipboard.
	Copy func(text string) tea.Cmd
}

// copyToClipboard tries the native Windows clipboard first and falls back to
// asking the terminal to set it with OSC 52, the same path the SSH key screen
// uses. Its result is a status line, so the user sees that it happened.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		if gitssh.CopyWin32(text) == nil {
			return uictx.StatusMsg{Text: "Copied to clipboard", Level: "success"}
		}
		return tea.BatchMsg{
			tea.SetClipboard(text),
			uictx.Status("success", "Copied to clipboard"),
		}
	}
}

// runHandle lets a screen's Stop, which must not block, cancel the work its
// commands started. It is a pointer inside a value screen so every copy of
// the screen shares it.
type runHandle struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

// newRunHandle returns a handle and the context it cancels.
func newRunHandle() (*runHandle, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	return &runHandle{cancel: cancel}, ctx
}

// stop cancels the run. It is safe to call twice and on a nil handle.
func (h *runHandle) stop() {
	if h == nil {
		return
	}
	h.mu.Lock()
	c := h.cancel
	h.mu.Unlock()
	if c != nil {
		c()
	}
}

// styleInput applies the theme to a text field so it reads like the rest of
// the screen: accent prompt, muted placeholder, plain body text.
func styleInput(ti textinput.Model, th *theme.Theme) textinput.Model {
	state := textinput.StyleState{
		Text: th.Base, Placeholder: th.Muted, Suggestion: th.Muted, Prompt: th.Accent,
	}
	st := ti.Styles()
	st.Focused, st.Blurred = state, state
	st.Cursor.Color = th.Palette.Accent
	st.Cursor.Blink = false
	ti.SetStyles(st)
	return ti
}

// newInput returns a text field of the given width and placeholder.
func newInput(width int, placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.Placeholder = placeholder
	ti.SetWidth(width)
	return ti
}

// spinnerEvery is how often a busy screen advances its spinner, in
// milliseconds.
const spinnerEvery = 120

// spinTick is tea.Tick, held in a variable so a test can stop the spinner from
// scheduling real timers.
var spinTick = tea.Tick

// explain turns an error into the words a person needs, using the error
// table. phase says whether it came before or during a copy; user is what
// was typed, for the Microsoft account hint. The raw text is kept for the
// line under the explanation.
func explain(err error, phase errmap.Phase, user string) (errmap.Entry, bool) {
	return errmap.Explain(err, phase, user)
}

// errorView draws a failure: the plain title, why, the steps, and the raw
// error small underneath, so a report can still quote it.
func errorView(ctx uictx.Context, err error, phase errmap.Phase, user string) string {
	th := ctx.Theme
	var b strings.Builder
	if e, ok := explain(err, phase, user); ok {
		b.WriteString(th.Danger.Render(ctx.Icons.Fail+" "+e.Title) + "\n\n")
		b.WriteString(ctx.Wrap(th.Base.Render(e.Why)) + "\n\n")
		for i, s := range e.Steps {
			b.WriteString(ctx.Wrap(th.Base.Render(strconv.Itoa(i+1)+". "+s)) + "\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString(th.Danger.Render(ctx.Icons.Fail+" Something went wrong") + "\n\n")
	}
	b.WriteString(ctx.Truncate(th.Muted.Render(errorDetail(err))))
	return b.String()
}

// errorDetail is the short technical line under an explanation. For a
// Windows error it is "System error 1219" and not the error's own text: the
// text is Windows' long sentence in the PC's language (and a different one
// again when the code runs anywhere but Windows), it repeats what the plain
// words above already say, and it was cut off with an ellipsis at any usual
// width. The number is short, the same on every PC, and is exactly what the
// troubleshooting pages are titled, so it is what a person can search for.
// Whatever the error was wrapped in ("copying: ") stays in front of it.
func errorDetail(err error) string {
	msg := oneLine(err.Error())
	code, ok := errmap.CodeOf(err)
	if !ok {
		return msg
	}
	detail := "System error " + strconv.Itoa(code)
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if prefix, found := strings.CutSuffix(msg, oneLine(errno.Error())); found {
			return prefix + detail
		}
	}
	return detail
}

// oneLine flattens an error text to a single line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

// commas writes a whole number with thousands separators, as "1,234".
func commas(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	digits := strconv.FormatInt(n, 10)
	var out []byte
	for i := range len(digits) {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, digits[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// fit cuts s to width columns with an ellipsis, on plain text.
func fit(s string, width int) string {
	if width <= 1 {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

// pickerConfig is the configuration the folder picker is built from: the
// user's default projects folder and nothing else. The picker's list of
// recent folders is the cleaner's "scanned recently" list, which is the wrong
// thing to offer as a place to share or to save into.
func pickerConfig(cfg config.Config) config.Config {
	c := config.Default()
	c.DefaultProjectsFolder = cfg.DefaultProjectsFolder
	return c
}
