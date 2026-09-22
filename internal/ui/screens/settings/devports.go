package settings

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// devPortsScreen edits the busy-dev-ports list as one line of comma- and
// range-separated numbers, e.g. "3000-3010, 5173".
type devPortsScreen struct {
	input  textinput.Model
	errMsg string
	saved  bool

	submit key.Binding
	reset  key.Binding
	back   key.Binding
}

// newDevPortsScreen returns the dev-ports sub-screen seeded from cfg.
func newDevPortsScreen(cfg config.Config) devPortsScreen {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.SetValue(formatDevPorts(cfg.DevPorts))
	ti.CursorEnd()
	return devPortsScreen{
		input:  ti,
		submit: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "save")),
		reset:  key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "reset to default")),
		back:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

// Init implements uictx.Screen.
func (s devPortsScreen) Init() tea.Cmd { return s.input.Focus() }

// Title implements uictx.Screen.
func (s devPortsScreen) Title() string { return "Dev ports" }

// ShortHelp implements uictx.Screen.
func (s devPortsScreen) ShortHelp() []key.Binding { return []key.Binding{s.submit, s.reset, s.back} }

// FullHelp implements uictx.Screen.
func (s devPortsScreen) FullHelp() [][]key.Binding {
	return [][]key.Binding{{s.submit, s.reset, s.back}}
}

// Update implements uictx.Screen.
func (s devPortsScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(km, s.reset):
			cfg := ctx.Config
			cfg.DevPorts = config.DefaultDevPorts()
			s.input.SetValue(formatDevPorts(cfg.DevPorts))
			s.input.CursorEnd()
			s.errMsg = ""
			s.saved = true
			return s, tea.Batch(uictx.SaveConfig(cfg), uictx.Status("success", "Reset to the default ports"))
		case key.Matches(km, s.submit):
			ports, err := parseDevPorts(s.input.Value())
			if err != nil {
				s.errMsg = err.Error()
				s.saved = false
				return s, nil
			}
			cfg := ctx.Config
			cfg.DevPorts = ports
			s.input.SetValue(formatDevPorts(ports))
			s.input.CursorEnd()
			s.errMsg = ""
			s.saved = true
			return s, tea.Batch(uictx.SaveConfig(cfg), uictx.Status("success", "Saved"))
		}
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	s.saved = false
	return s, cmd
}

// View implements uictx.Screen.
func (s devPortsScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(th.Muted.Render(ctx.Wrap(
		`Comma-separated ports and ranges, e.g. "3000-3010, 5173, 8080". This is the list the busy-ports view checks.`)))
	b.WriteString("\n\n")
	b.WriteString(s.input.View())

	if s.errMsg != "" {
		b.WriteString("\n\n")
		b.WriteString(th.Danger.Render(ctx.Icons.Warn + " " + s.errMsg))
	} else if s.saved {
		b.WriteString("\n\n")
		b.WriteString(th.Success.Render(ctx.Icons.Tick + " Saved"))
	}
	return b.String()
}

// parseDevPorts parses the dev-ports text field into a sorted, deduplicated
// list of valid TCP/UDP ports (1-65535). Each comma-separated token is
// either a single port ("5173") or an inclusive range ("3000-3010").
func parseDevPorts(s string) ([]int, error) {
	seen := make(map[int]bool)
	var out []int

	for _, field := range strings.Split(s, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(field, "-")
		if isRange {
			loN, hiN, err := parsePortRange(field, lo, hi)
			if err != nil {
				return nil, err
			}
			for p := loN; p <= hiN; p++ {
				addPort(&out, seen, p)
			}
			continue
		}
		n, err := strconv.Atoi(field)
		if err != nil {
			return nil, fmt.Errorf("invalid port %q", field)
		}
		if err := validatePort(n); err != nil {
			return nil, err
		}
		addPort(&out, seen, n)
	}

	if len(out) == 0 {
		return nil, errors.New("enter at least one port")
	}
	sort.Ints(out)
	return out, nil
}

// parsePortRange parses and validates the two halves of "lo-hi", where field
// is the original token, used for its error text.
func parsePortRange(field, lo, hi string) (int, int, error) {
	loN, err := strconv.Atoi(strings.TrimSpace(lo))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid port range %q", field)
	}
	hiN, err := strconv.Atoi(strings.TrimSpace(hi))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid port range %q", field)
	}
	if err := validatePort(loN); err != nil {
		return 0, 0, err
	}
	if err := validatePort(hiN); err != nil {
		return 0, 0, err
	}
	if loN > hiN {
		return 0, 0, fmt.Errorf("invalid port range %q: the start is after the end", field)
	}
	return loN, hiN, nil
}

// validatePort reports whether n is a valid TCP/UDP port number.
func validatePort(n int) error {
	if n < 1 || n > 65535 {
		return fmt.Errorf("port %d is out of range (1-65535)", n)
	}
	return nil
}

// addPort appends p to out if it has not been seen yet.
func addPort(out *[]int, seen map[int]bool, p int) {
	if seen[p] {
		return
	}
	seen[p] = true
	*out = append(*out, p)
}

// formatDevPorts renders a port list back into the compact comma/range text
// the input field shows, collapsing runs of consecutive ports into ranges.
func formatDevPorts(ports []int) string {
	if len(ports) == 0 {
		return ""
	}
	sorted := make([]int, len(ports))
	copy(sorted, ports)
	sort.Ints(sorted)

	uniq := make([]int, 0, len(sorted))
	for i, p := range sorted {
		if i == 0 || p != sorted[i-1] {
			uniq = append(uniq, p)
		}
	}

	var parts []string
	for i := 0; i < len(uniq); {
		j := i
		for j+1 < len(uniq) && uniq[j+1] == uniq[j]+1 {
			j++
		}
		if j > i {
			parts = append(parts, fmt.Sprintf("%d-%d", uniq[i], uniq[j]))
		} else {
			parts = append(parts, strconv.Itoa(uniq[i]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}
