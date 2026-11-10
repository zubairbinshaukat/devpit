//go:build windows

package elevate

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Every hotspot script parses in this PC's Windows PowerShell 5.1. Only the
// parser runs: the script is handed to Parser.ParseInput as a string and is
// never executed, so no hotspot, rule or setting is touched.
func TestTheHotspotScriptsParseInWindowsPowerShell(t *testing.T) {
	scripts := map[string]string{
		"probe":       scriptHotspotProbe,
		"timeout-off": scriptHotspotTimeoutOff,
		"timeout-on":  scriptHotspotTimeoutOn,
		"start":       scriptHotspotStart,
		"stop":        fmt.Sprintf(scriptHotspotStop, psString("Bob’s hotspot"), psString("name")),
		"restore":     scriptHotspotRestore,
		"rule-add":    fmt.Sprintf(scriptHotspotRuleAdd, psString("devpit-hs-abcd1234"), psString(hsRuleDescription), psString("192.168.137.0/24")),
		"rule-remove": fmt.Sprintf(scriptHotspotRuleRemove, psString("devpit-hs-abcd1234"), psString(hsRuleDescription)),
		"free-addr":   fmt.Sprintf(scriptFreeAddr, psString("192.168.137.1")),
	}
	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(script, "%!") {
				t.Fatalf("the script was formatted wrongly:\n%s", script)
			}
			check := `$tok=$null; $errs=$null
[void][System.Management.Automation.Language.Parser]::ParseInput(` + psString(script) + `, [ref]$tok, [ref]$errs)
foreach($e in $errs){ Write-Output ('PERR=' + $e.Extent.StartLineNumber + ': ' + $e.Message) }
Write-Output 'PARSED=1'`
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			var out, errs []string
			code, err := DefaultExecutor{}.Exec(ctx, psArgv(check), func(stream, text string) {
				if stream == "stderr" {
					errs = append(errs, text)
				} else {
					out = append(out, text)
				}
			})
			if err != nil || code != 0 {
				t.Skipf("cannot run Windows PowerShell here (%d, %v): %s", code, err, strings.Join(errs, " "))
			}
			if !contains(out, "PARSED=1") {
				t.Fatalf("the parser did not run: %v %v", out, errs)
			}
			for _, l := range out {
				if strings.HasPrefix(l, "PERR=") {
					t.Errorf("%s: %s", name, l)
				}
			}
		})
	}
}

// runPS runs a read-only check script in Windows PowerShell 5.1, with stdin
// on its standard input, and skips when PowerShell cannot run here.
func runPS(t *testing.T, script, stdin string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var out, errs []string
	code, err := DefaultExecutor{}.ExecInput(ctx, psArgv(script), stdin, func(stream, text string) {
		if stream == "stderr" {
			errs = append(errs, text)
		} else {
			out = append(out, text)
		}
	})
	if err != nil || code != 0 {
		t.Skipf("cannot run Windows PowerShell here (%d, %v): %s", code, err, strings.Join(errs, " "))
	}
	return out
}

// The timeout members the scripts call on the type are static members of
// NetworkOperatorTetheringManager on this Windows. Only reflection runs:
// nothing is called, so no setting is read or changed.
func TestTheTimeoutMembersAreStaticHere(t *testing.T) {
	out := runPS(t, `$TM=[Windows.Networking.NetworkOperators.NetworkOperatorTetheringManager,Windows.Networking.NetworkOperators,ContentType=WindowsRuntime]
foreach($n in @('IsNoConnectionsTimeoutEnabled','EnableNoConnectionsTimeoutAsync','DisableNoConnectionsTimeoutAsync')){
  $m=$TM.GetMethod($n, [Type]::EmptyTypes)
  if($null -ne $m -and $m.IsStatic){ Write-Output ('STATIC=' + $n) }
}`, "")
	for _, n := range []string{"IsNoConnectionsTimeoutEnabled", "EnableNoConnectionsTimeoutAsync", "DisableNoConnectionsTimeoutAsync"} {
		if !contains(out, "STATIC="+n) {
			t.Errorf("%s is not a static member here: %v", n, out)
		}
	}
}

// A value written by hsInput reaches the script through ReadIn exactly, by
// standard input, with nothing on the command line.
func TestReadInGetsTheValuesFromStandardInput(t *testing.T) {
	values := []string{"temp", "Bob’s hotspot", goodPassphrase, "", "1"}
	script := hsReadInput + `foreach($i in 0..4){ $v=ReadIn; Write-Output ('V=' + [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($v))) }`
	out := runPS(t, script, hsInput(values...))
	var got []string
	for _, l := range out {
		if v, ok := strings.CutPrefix(l, "V="); ok {
			got = append(got, v)
		}
	}
	want := strings.Split(strings.TrimSuffix(hsInput(values...), "\n"), "\n")
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ReadIn read %v, want %v", got, want)
	}
}
