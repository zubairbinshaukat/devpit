package filepicker

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseBrowseOutputTrimsWhateverPowerShellAdds(t *testing.T) {
	cases := map[string]string{
		"":                            "",
		"\r\n":                        "",
		`D:\Downloads\a.zip`:          `D:\Downloads\a.zip`,
		"D:\\Downloads\\a.zip\r\n":    `D:\Downloads\a.zip`,
		"\uFEFFD:\\Downloads\\a.zip":  `D:\Downloads\a.zip`,
		"D:\\Down loads\\a b.7z.001 ": `D:\Down loads\a b.7z.001`,
	}
	for in, want := range cases {
		if got := parseBrowseOutput([]byte(in)); got != want {
			t.Errorf("parseBrowseOutput(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyBrowseErr(t *testing.T) {
	if err := classifyBrowseErr(context.DeadlineExceeded, context.DeadlineExceeded, ""); !errors.Is(err, errBrowseTimeout) {
		t.Errorf("a passed deadline = %v, want the timeout", err)
	}
	runErr := errors.New("exit status 1")
	err := classifyBrowseErr(runErr, nil, "  Add-Type : some assembly problem  ")
	if !errors.Is(err, errBrowseFailed) || !errors.Is(err, runErr) {
		t.Errorf("a failed run = %v, want it to wrap both errors", err)
	}
	// Whatever PowerShell said stays in the error for a log, and never
	// reaches the screen: the picker words both failures itself.
	for _, e := range []error{err, errBrowseTimeout} {
		if got := Refusal(e); strings.Contains(got, "Add-Type") || strings.Contains(got, "exit status") || got == "" {
			t.Errorf("Refusal(%v) = %q", e, got)
		}
	}
}

// TestTheDialogScriptTakesNoInput pins what keeps the script safe: it is a
// constant, the start folder reaches it only through the environment, and a
// network folder is never handed over at all.
func TestTheDialogScriptTakesNoInput(t *testing.T) {
	for _, want := range []string{
		"System.Windows.Forms.OpenFileDialog",
		"'Archives|*.zip;*.7z;*.rar;*.001;*.r00|All files|*.*'",
		"$env:" + browseStartEnv,
		"$d.Multiselect = $false",
	} {
		if !strings.Contains(browseScript, want) {
			t.Errorf("the dialog script does not have %q", want)
		}
	}
	if !isUNC(`\\nas\share`) || !isUNC(`//nas/share`) || isUNC(`D:\Downloads`) {
		t.Error("isUNC is wrong")
	}
}

// TestRunPowerShellScriptMechanics runs a real, hidden PowerShell with a
// script that shows no dialog: output is captured, the environment is passed,
// stderr stays apart, and a failing script is an error.
func TestRunPowerShellScriptMechanics(t *testing.T) {
	if testing.Short() {
		t.Skip("starts PowerShell")
	}
	ctx, cancel := context.WithTimeout(context.Background(), browseTimeout)
	defer cancel()
	out, _, err := runPowerShellScript(ctx,
		"[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; [Console]::Error.WriteLine('a warning'); [Console]::Out.Write($env:"+browseStartEnv+")",
		[]string{browseStartEnv + `=D:\Downloads\é`})
	if err != nil {
		t.Skipf("PowerShell could not be started here: %v", err)
	}
	if got := parseBrowseOutput(out); got != `D:\Downloads\é` {
		t.Errorf("stdout = %q", got)
	}
	if _, _, err := runPowerShellScript(ctx, "exit 3", nil); err == nil {
		t.Error("a script that exits 3 was not an error")
	}
}
