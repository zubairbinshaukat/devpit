package managers_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
)

// Every row is complete and spelled the way its source spells it, and no
// code appears twice for the same manager.
func TestCodeTableIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	hexRE := regexp.MustCompile(`^0x[0-9A-F]{8}$`)
	for _, c := range managers.Codes() {
		if c.Symbol == "" || c.Label == "" || c.Meaning == "" || c.NextStep == "" {
			t.Errorf("%s has an empty field: %+v", c.Hex, c)
		}
		if strings.ContainsAny(c.Label+c.Meaning+c.NextStep, "{}") {
			t.Errorf("%s has an unfilled placeholder: %+v", c.Hex, c)
		}
		if c.Family == managers.FamilyInstaller {
			if len(c.Managers) == 0 {
				t.Errorf("installer code %s must name the managers it applies to", c.Hex)
			}
		} else if !hexRE.MatchString(c.Hex) {
			t.Errorf("%s is not spelled 0xUPPER8HEX", c.Hex)
		}
		if strings.HasSuffix(c.Label, ".") {
			t.Errorf("%s: a label has no full stop: %q", c.Hex, c.Label)
		}
		key := c.Hex + "|" + strings.Join(c.Managers, ",")
		if seen[key] {
			t.Errorf("%s appears twice", c.Hex)
		}
		seen[key] = true
	}
	if len(seen) < 60 {
		t.Errorf("only %d codes listed; the table was cut short", len(seen))
	}
}

func TestCrashCodesInBothSignednesses(t *testing.T) {
	for _, code := range []uint32{0xC0000409, 0xC0000005, 0xC0000374, 0xC00000FD, 0xE06D7363} {
		for _, exit := range []int{int(code), hresult(code)} {
			v := managers.Explain("winget", exit, nil)
			if v.Kind != managers.VerdictCrashed {
				t.Errorf("exit %d: kind = %v, want crashed", exit, v.Kind)
			}
			want := "crashed (" + hexOf(code) + ")"
			if v.Text != want || v.Next == "" {
				t.Errorf("exit %d: {%q %q}, want text %q and a next step", exit, v.Text, v.Next, want)
			}
		}
	}
	// The exact number from the bug report.
	if v := managers.Explain("npm", 3221226505, nil); v.Text != "crashed (0xC0000409)" {
		t.Errorf("3221226505 = %q", v.Text)
	}
}

func hexOf(code uint32) string {
	const digits = "0123456789ABCDEF"
	b := []byte("0x00000000")
	for i := 9; i >= 2; i-- {
		b[i] = digits[code&0xF]
		code >>= 4
	}
	return string(b)
}

func TestCtrlCIsStoppedNotCrashed(t *testing.T) {
	v := managers.Explain("choco", 0xC000013A, nil)
	if v.Kind != managers.VerdictCancelled || !strings.HasPrefix(v.Text, "stopped early") {
		t.Errorf("got {%v %q}", v.Kind, v.Text)
	}
}

func TestNeedsAdminAndInUseNameTheApp(t *testing.T) {
	v := managers.ExplainApp("winget", "Contoso App", hresult(0x80073D28), nil)
	if v.Kind != managers.VerdictNeedsAdmin || v.Text != "needs admin rights" {
		t.Errorf("0x80073D28 = {%v %q}", v.Kind, v.Text)
	}
	v = managers.ExplainApp("winget", "Contoso App", hresult(0x80073D02), nil)
	if v.Kind != managers.VerdictInUse || v.Text != "in use, close Contoso App and retry" || !strings.Contains(v.Next, "Close Contoso App") {
		t.Errorf("0x80073D02 = {%v %q %q}", v.Kind, v.Text, v.Next)
	}
	if v := managers.Explain("winget", hresult(0x80073D02), nil); v.Text != "in use, close it and retry" {
		t.Errorf("without a name: %q", v.Text)
	}
}

func TestMSIXCodes(t *testing.T) {
	for _, code := range []uint32{0x80073CF0, 0x80073CF3, 0x80073CF9, 0x80073CFB, 0x80073CF4, 0x80073CF6, 0x80073D01} {
		v := managers.Explain("winget", hresult(code), nil)
		if v.Kind != managers.VerdictFailed || strings.HasPrefix(v.Text, "error 0x") || v.Next == "" {
			t.Errorf("0x%X = {%v %q %q}, want plain words and a next step", code, v.Kind, v.Text, v.Next)
		}
	}
}

// MSI codes are choco's own exit codes; winget reaches them only through
// the installer's exit code line.
func TestInstallerCodes(t *testing.T) {
	if v := managers.Explain("choco", 1603, nil); v.Text != "installer failed (1603)" || v.Next == "" {
		t.Errorf("choco 1603 = %+v", v)
	}
	if v := managers.Explain("choco", 1618, nil); v.Text != "another install is running" {
		t.Errorf("choco 1618 = %+v", v)
	}
	if v := managers.Explain("choco", 1602, nil); v.Kind != managers.VerdictCancelled {
		t.Errorf("choco 1602 = %+v", v)
	}
	for _, code := range []int{1641, 3010} {
		if v := managers.Explain("choco", code, nil); v.Kind != managers.VerdictRestart {
			t.Errorf("choco %d = %+v", code, v)
		}
	}
	// npm's exit 1603 is npm's own business, not an installer's.
	if v := managers.Explain("npm", 1603, nil); v.Text != "exit code 1603" {
		t.Errorf("npm 1603 = %+v", v)
	}
	lines := []string{"Installer failed with exit code: 1603"}
	if v := managers.Explain("winget", hresult(0x8A150115), lines); v.Text != "installer failed (1603)" {
		t.Errorf("winget custom error with installer line = %+v", v)
	}
	lines = []string{"Installation failed with error code: 0x80073d28"}
	if v := managers.Explain("winget", hresult(0x8A150003), lines); v.Kind != managers.VerdictNeedsAdmin {
		t.Errorf("winget general failure naming 0x80073d28 = %+v", v)
	}
}

// The lines winget really prints (microsoft/WSL#41645): the MSIX error comes
// as hex after "exit code:", which must not be read as the decimal "0".
func TestWingetInstallerExitLine(t *testing.T) {
	cases := []struct {
		line string
		kind managers.VerdictKind
		text string
	}{
		{"Installer failed with exit code: 0x80073d28 : The package installation failed because administrator privileges are required.", managers.VerdictNeedsAdmin, "needs admin rights"},
		{"Installer failed with exit code: 0x80073d02 : The package could not be installed because resources it modifies are currently in use.", managers.VerdictInUse, "in use, close Contoso App and retry"},
		// An installer that crashed: the unsigned status, and the signed
		// spelling of the same DWORD.
		{"Installer failed with exit code: 3221226505", managers.VerdictCrashed, "crashed (0xC0000409)"},
		{"Installer failed with exit code: -1073741819", managers.VerdictCrashed, "crashed (0xC0000005)"},
		{"Installer failed with exit code: 3221225781", managers.VerdictFailed, "could not start (0xC0000135)"},
		{"Installer failed with exit code: 1603", managers.VerdictFailed, "installer failed (1603)"},
	}
	for _, c := range cases {
		// 0x8A150006 is winget's "running ShellExecute failed", the code it
		// exits with when the installer it ran failed.
		v := managers.ExplainApp("winget", "Contoso App", hresult(0x8A150006), []string{"Starting package install...", c.line})
		if v.Kind != c.kind || v.Text != c.text {
			t.Errorf("%q = {%v %q}, want {%v %q}", c.line, v.Kind, v.Text, c.kind, c.text)
		}
	}
	// A number that is no code keeps winget's own.
	v := managers.Explain("winget", hresult(0x8A150006), []string{"Installer failed with exit code: 5"})
	if v.Text != "error 0x8A150006" {
		t.Errorf("unknown installer code = %+v", v)
	}
}

// Each NTSTATUS a crashing process exits with reads as what it is: the
// values and names are ntstatus.h's (MS-ERREF 2.3.1).
func TestNTSTATUSMapping(t *testing.T) {
	cases := map[uint32]struct {
		kind  managers.VerdictKind
		label string
	}{
		0xC0000005: {managers.VerdictCrashed, "crashed"},
		0xC0000409: {managers.VerdictCrashed, "crashed"},
		0xC0000374: {managers.VerdictCrashed, "crashed"},
		0xC00000FD: {managers.VerdictCrashed, "crashed"},
		0xC0000135: {managers.VerdictFailed, "could not start"},
		0xC000007B: {managers.VerdictFailed, "could not start"},
		0xC0000017: {managers.VerdictFailed, "ran out of memory"},
		0xC000013A: {managers.VerdictCancelled, "stopped early"},
	}
	for code, want := range cases {
		for _, exit := range []int{int(code), hresult(code)} {
			v := managers.Explain("scoop", exit, nil)
			if v.Kind != want.kind || v.Text != want.label+" ("+hexOf(code)+")" || v.Next == "" {
				t.Errorf("exit %d = {%v %q %q}, want %v %q", exit, v.Kind, v.Text, v.Next, want.kind, want.label)
			}
		}
	}
	// An unmapped status still reads as hex, never as a negative number.
	if v := managers.Explain("npm", hresult(0xC0000999), nil); v.Text != "error 0xC0000999" {
		t.Errorf("unmapped status = %q", v.Text)
	}
}

func TestRetryFor(t *testing.T) {
	byName := map[string]managers.Manager{}
	for _, m := range managers.All() {
		byName[m.Name()] = m
	}
	r := managers.RetryFor(byName["winget"], "Git.Git", false, false)
	if !strings.HasPrefix(r.Command, "winget upgrade --id Git.Git") || r.Note != "" {
		t.Errorf("winget = %+v", r)
	}
	if rc := managers.RetryFor(byName["scoop"], "git", false, false); rc.Command != "scoop update git" {
		t.Errorf("scoop = %+v", rc)
	}
	if rc := managers.RetryFor(byName["choco"], "git", false, false); rc.Command != "choco upgrade git -y" || !strings.Contains(rc.Note, "administrator") {
		t.Errorf("choco = %+v", rc)
	}
	r = managers.RetryFor(byName["npm"], "pnpm", false, false)
	if r.Command != "npm install -g pnpm@latest" || !strings.Contains(r.Note, "install it again") || !strings.Contains(r.Note, "npm install -g pnpm") {
		t.Errorf("npm = %+v", r)
	}
	if r := managers.RetryFor(byName["winget"], "Git.Git", true, false); !strings.HasPrefix(r.Command, "winget install --id Git.Git") {
		t.Errorf("install = %+v", r)
	}
	if r := managers.RetryFor(byName["scoop"], "", false, true); !strings.Contains(r.Command, " && ") {
		t.Errorf("all = %+v", r)
	}
}
