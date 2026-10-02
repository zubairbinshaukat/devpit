package devpit

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// installerScript is web/install.ps1, from this package's folder.
var installerScript = filepath.Join("..", "..", "web", "install.ps1")

// installerProbe parses install.ps1 as UTF-8 text (failing on any parse error), loads only
// its Get-AgentAction and Test-Yes functions (nothing else in the script
// runs, so nothing is installed), and prints one answer per case.
const installerProbe = `
$ErrorActionPreference = 'Stop'
$tokens = $null; $errs = $null
$text = [IO.File]::ReadAllText($env:DEVPIT_TEST_SCRIPT, [Text.Encoding]::UTF8)  # as irm | iex hands it over
$ast = [System.Management.Automation.Language.Parser]::ParseInput($text, [ref]$tokens, [ref]$errs)
if ($errs.Count -gt 0) { "PARSE ERROR: $($errs[0].Message) at line $($errs[0].Extent.StartLineNumber)"; exit 1 }
foreach ($name in 'Get-AgentAction', 'Test-Yes') {
  $fn = $ast.Find({ param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq $name }, $true)
  if (-not $fn) { "MISSING $name"; exit 1 }
  . ([scriptblock]::Create($fn.Extent.Text))
}
function S($claude, $state, $can, [string[]]$targets) {
  [pscustomobject]@{ claude_code = $claude; state = $state; can_install = $can; targets = @($targets | ForEach-Object { [pscustomobject]@{ state = $_ } }) }
}
$none = S $true 'not installed' $true @('create')
$older = S $true 'update available' $true @('update')
$current = S $true 'installed' $false @('up to date')
$foreign = S $true 'a different devpit skill is in the way' $false @('not Devpit''s')
$noClaude = S $false 'Claude Code not found' $false @('create')
"fresh-interactive=" + (Get-AgentAction $none $false $false $true)
"fresh-noninteractive=" + (Get-AgentAction $none $false $false $false)
"fresh-noagent=" + (Get-AgentAction $none $false $true $true)
"fresh-noclaude=" + (Get-AgentAction $noClaude $false $false $true)
"fresh-nostatus=" + (Get-AgentAction $null $false $false $true)
"fresh-foreign=" + (Get-AgentAction $foreign $false $false $true)
"update-missing=" + (Get-AgentAction $none $true $false $true)
"update-older=" + (Get-AgentAction $older $true $false $false)
"update-current=" + (Get-AgentAction $current $true $false $true)
"update-noagent=" + (Get-AgentAction $older $true $true $true)
foreach ($a in @('', ' ', 'n', 'no', 'yes please', 'ok', 'y', 'Y', 'yes', ' YES ')) { "yes[$a]=" + (Test-Yes $a) }
"yes[null]=" + (Test-Yes $null)
`

// The installer never adds the AI agent skill without a yes: it asks only on
// a fresh install with Claude Code present and a person at the keyboard,
// Enter and anything but y/yes is No, a run with nobody there or -NoAgent
// never asks, and an update only refreshes a skill Devpit wrote before. The
// decision runs in Windows PowerShell 5.1 and in PowerShell 7 when present,
// which also proves the script parses in both.
func TestInstallerNeverAddsTheSkillWithoutAYes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell is the installer's shell")
	}
	text, err := os.ReadFile(installerScript)
	if os.IsNotExist(err) {
		t.Skip("web/install.ps1 is not in this tree")
	}
	if err != nil {
		t.Fatal(err)
	}
	script := string(text)
	for _, want := range []string{
		"[switch]$NoAgent",
		"Read-Host '  Let AI agents (Claude Code) use Devpit? [y/N]'",
		"if (-not (Test-Yes $answer)) { $agentAction = 'skip:no' }",
		"Invoke-Devpit @('agent', 'remove', '--yes')",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("install.ps1 lacks %q", want)
		}
	}
	// The skill is written only for 'ask' (after the yes above) and 'refresh'.
	if n := strings.Count(script, "@('agent', 'install', '--yes')"); n != 2 {
		t.Errorf("agent install appears %d times; want exactly the ask and refresh branches", n)
	}
	// Uninstall removes the skill while devpit.exe is still there.
	if strings.Index(script, "'agent', 'remove'") > strings.Index(script, "Remove-Item -Recurse -Force $Dir") {
		t.Error("the skill must be removed before devpit.exe is")
	}

	want := map[string]string{
		"fresh-interactive": "ask", "fresh-noninteractive": "skip:non-interactive", "fresh-noagent": "skip:flag",
		"fresh-noclaude": "skip:no-claude", "fresh-nostatus": "skip:no-claude", "fresh-foreign": "skip:foreign",
		"update-missing": "skip:update", "update-older": "refresh", "update-current": "skip:current", "update-noagent": "skip:flag",
		"yes[]": "False", "yes[ ]": "False", "yes[n]": "False", "yes[no]": "False", "yes[yes please]": "False", "yes[ok]": "False",
		"yes[y]": "True", "yes[Y]": "True", "yes[yes]": "True", "yes[ YES ]": "True", "yes[null]": "False",
	}
	ran := 0
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		bin, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		ran++
		abs, _ := filepath.Abs(installerScript)
		probe := filepath.Join(t.TempDir(), "probe.ps1")
		if err = os.WriteFile(probe, []byte(installerProbe), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bin, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", probe) //nolint:gosec // test
		cmd.Env = append(os.Environ(), "DEVPIT_TEST_SCRIPT="+abs)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", shell, err, out)
		}
		got := map[string]string{}
		for _, l := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
			if k, v, ok := strings.Cut(l, "="); ok {
				got[k] = strings.TrimSpace(v)
			}
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q", shell, k, got[k], v)
			}
		}
	}
	if ran == 0 {
		t.Skip("no PowerShell on PATH")
	}
}
