package ports

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	engine "github.com/zubairbinshaukat/devpit/internal/ports"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// accessDeniedStatus is the footer status shown when killing a process needs
// more privilege than Devpit's TUI runs with. The elevated worker that would
// carry this out lands in milestone 5; for now the screen just says so.
const accessDeniedStatus = "needs admin — elevation comes in milestone 5"

// parsePort validates a digit string as a port: not empty, all digits,
// 1-65535.
func parsePort(digits string) (uint16, error) {
	if digits == "" {
		return 0, errors.New("enter a port number")
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, errors.New("a port is digits only")
	}
	if n < 1 || n > 65535 {
		return 0, errors.New("a port is between 1 and 65535")
	}
	return uint16(n), nil
}

// configPorts converts the config's editable dev-port list to the engine's
// type, falling back to ports.DefaultDevPorts when the config list is empty
// or has nothing usable in it.
func configPorts(devPorts []int) []uint16 {
	out := make([]uint16, 0, len(devPorts))
	for _, p := range devPorts {
		if p > 0 && p <= 65535 {
			out = append(out, uint16(p))
		}
	}
	if len(out) == 0 {
		return engine.DefaultDevPorts()
	}
	return out
}

// isNodeProcess reports whether name (with or without ".exe") is one of the
// processes the node-processes flow lists.
func isNodeProcess(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimSuffix(n, ".exe")
	switch n {
	case "node", "npm", "pnpm", "yarn":
		return true
	}
	return false
}

// describeKillErr turns a kill error into a reason for the summary card, and
// reports whether it was an access-denied failure (the UI then shows the
// elevation status instead of treating it like an ordinary skip).
func describeKillErr(err error) (reason string, accessDenied bool) {
	var protErr *engine.ProtectedError
	if errors.As(err, &protErr) {
		return protErr.Reason, false
	}
	var deniedErr *engine.AccessDeniedError
	if errors.As(err, &deniedErr) {
		return accessDeniedStatus, true
	}
	return err.Error(), false
}

// displayName is the name shown for a process: its name, or its PID when the
// name could not be resolved.
func displayName(p engine.Process) string {
	if p.Name != "" {
		return p.Name
	}
	return fmt.Sprintf("PID %d", p.PID)
}

// rowLabel is the name shown for a table row in a summary's skipped list.
func rowLabel(r row) string {
	name := r.Name
	if name == "" {
		name = fmt.Sprintf("PID %d", r.PID)
	}
	return fmt.Sprintf("%s (PID %d)", name, r.PID)
}

// formatAddr joins a local address and port the way the listener table shows
// it.
func formatAddr(ip string, port uint16) string {
	return net.JoinHostPort(ip, strconv.Itoa(int(port)))
}

// padColumns pads each cell to its column width and joins them with two
// spaces, for the hand-rolled listener table.
func padColumns(cells []string, widths []int) string {
	var b strings.Builder
	for i, c := range cells {
		w := 0
		if i < len(widths) {
			w = widths[i]
		}
		b.WriteString(padRight(c, w))
		if i < len(cells)-1 {
			b.WriteString("  ")
		}
	}
	return b.String()
}

// padRight right-pads s with spaces to width w, measured in runes. It never
// truncates: a cell wider than its column simply pushes the next one right.
func padRight(s string, w int) string {
	n := len([]rune(s))
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

// renderTreeToggle draws the "kill process tree" line shown under a confirm
// dialog when ports.OffersTree applies. parentName and descendants are only
// used by the single-port flow, which knows the exact parent and how many
// other processes it would also stop; the list flow passes "" and 0.
func (m Model) renderTreeToggle(ctx uictx.Context, on bool, parentName string, descendants int) string {
	th := ctx.Theme
	box := ctx.Icons.Unchecked
	if on {
		box = ctx.Icons.Checked
	}
	label := "[t] Kill process tree"
	if parentName != "" {
		if descendants > 0 {
			label = fmt.Sprintf("[t] Kill process tree (also stops %d other process(es) started by %s)", descendants, parentName)
		} else {
			label = fmt.Sprintf("[t] Kill process tree (stops %s too)", parentName)
		}
	}
	style := th.Base
	if on {
		style = th.Accent
	}
	return style.Render(box + " " + label)
}
