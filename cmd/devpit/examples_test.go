package devpit

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Every command a user can see ends its help with real examples, and every
// example line is a devpit command line. Agents learn the command line from
// --help, so a command without one is a command they will guess at. Cobra's
// own help and completion commands are not ours and are left out.
func TestEveryCommandHasAnExample(t *testing.T) {
	t.Parallel()
	root := NewRootCmd()
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			return
		}
		ex := strings.TrimSpace(c.Example)
		if ex == "" {
			t.Errorf("%q has no Example", c.CommandPath())
		}
		for _, line := range strings.Split(ex, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if line != root.Name() && !strings.HasPrefix(line, root.Name()+" ") {
				t.Errorf("%q example %q is not a %s command line", c.CommandPath(), line, root.Name())
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
