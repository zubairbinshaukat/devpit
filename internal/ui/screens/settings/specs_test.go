package settings

import (
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/sysinfo"
	"github.com/zubairbinshaukat/devpit/internal/sysinfo/sysinfofake"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/specs"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// PC specs is the first row; Enter opens the page, and nothing is read
// until the page itself starts: not by Settings, not by the row.
func TestPCSpecsRowOpensThePageAndReadsNothingYet(t *testing.T) {
	src := sysinfofake.New(sysinfofake.Good())
	made := 0
	m := NewWith(Options{
		Skill: opener(skillWith(service.AgentCreate, service.AgentCreate)), Tick: noTick, Version: "0.5.0",
		Specs: specs.Options{Source: func() sysinfo.Source { made++; return src }},
	})
	ctx := sized(config.Default(), icons.Unicode(), 100, 30)
	if m.lay(ctx).Cursor != rowSpecs {
		t.Fatalf("the first row is %q", m.lay(ctx).Cursor)
	}
	_, cmd := step(t, m, ctx, keyPress("enter"))
	var pushed uictx.Screen
	for _, msg := range flattenCmd(cmd) {
		if p, ok := msg.(uictx.PushScreenMsg); ok {
			pushed = p.Screen
		}
	}
	if pushed == nil || pushed.Title() != "PC specs" {
		t.Fatalf("enter pushed %v", pushed)
	}
	if made != 0 || src.Calls() != 0 {
		t.Fatalf("opening read the PC: %d sources, %d reads", made, src.Calls())
	}
}
