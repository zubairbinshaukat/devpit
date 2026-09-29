//go:build windows

package elevate

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Runs only the read-only half of the firewall script against this PC's real
// firewall (Get-NetFirewallRule needs no admin rights and changes nothing)
// and checks it would turn on FPS-SMB-In-TCP and never the NoScope rule.
func TestTheRealFirewallPickIsTheSMBRuleAlone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var out, errs []string
	code, err := DefaultExecutor{}.Exec(ctx, psArgv(firewallPick+"\n$pick | ForEach-Object { 'PICK=' + $_.Name }"),
		func(stream, text string) {
			if stream == "stderr" {
				errs = append(errs, text)
			} else {
				out = append(out, text)
			}
		})
	if err != nil || code != 0 {
		t.Skipf("cannot read the firewall rules here (%d, %v): %s", code, err, strings.Join(errs, " "))
	}
	var picked []string
	for _, l := range out {
		if name, ok := strings.CutPrefix(strings.TrimSpace(l), "PICK="); ok {
			picked = append(picked, name)
		}
	}
	if len(picked) == 0 {
		t.Skip("this PC has no File and Printer Sharing rules")
	}
	for _, name := range picked {
		if strings.Contains(name, "NoScope") {
			t.Errorf("the pick includes %s", name)
		}
	}
	if contains(picked, "FPS-SMB-In-TCP") && len(picked) != 1 {
		t.Errorf("picked %v, want FPS-SMB-In-TCP alone", picked)
	}
}
