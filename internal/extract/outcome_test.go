package extract

import "testing"

const solidErr = "error: this RAR archive is solid: each file is compressed together with the files before it, so it can only be decoded from the start in one pass. Its space could not be released until the whole archive is extracted, and an interrupted extraction could not resume, so it cannot be extracted in place."

// Safety rule 84: the same text with different exit codes gives different
// verdicts, and the same exit code with any text gives the same one.
func TestVerdictComesOnlyFromTheExitCode(t *testing.T) {
	texts := []string{
		"", "error: not enough space", "Insufficient disk space: 1.0 GB more is required",
		"note: fits", "All entries are already extracted", "error: the archive is damaged",
	}
	want := map[int]Verdict{0: VerdictFits, 2: VerdictBadArgs, 3: VerdictNoSpace, 4: VerdictDamaged, 5: VerdictNeedPassword, 7: VerdictFailed, 130: VerdictFailed, -1: VerdictFailed}
	for exit, v := range want {
		for _, text := range texts {
			if got := mapCheck(exit, text, false, ModeInPlace); got != v {
				t.Errorf("exit %d text %q -> %v, want %v", exit, text, got, v)
			}
		}
	}
	// Exit 1 is Failed unless it is the solid refusal in place.
	if mapCheck(1, "error: archive damaged", false, ModeInPlace) != VerdictFailed {
		t.Error("exit 1")
	}
	if mapCheck(1, solidErr, false, ModeInPlace) != VerdictSolid || mapCheck(1, solidErr, false, ModeKeepArchive) != VerdictFailed {
		t.Error("solid")
	}
	// A path with the word "solid" in it is not the solid refusal.
	if mapCheck(1, `error: reading D:\Backups\solid-state.zip: access denied`, false, ModeInPlace) != VerdictFailed {
		t.Error("a path holding 'solid' read as a solid RAR")
	}
	if mapCheck(5, "", true, ModeInPlace) != VerdictWrongPassword {
		t.Error("wrong password")
	}
}

// Every row of section 6.6.
func TestClassifyTable(t *testing.T) {
	cases := []struct {
		exit   int
		killed bool
		mode   Mode
		pw     bool
		stderr string
		want   Kind
	}{
		{0, false, ModeInPlace, false, "", KindDone},
		{0, true, ModeInPlace, false, "", KindStopped},
		{1, true, ModeInPlace, false, "", KindStopped},
		{130, true, ModeInPlace, false, "", KindStopped},
		{130, false, ModeInPlace, false, "", KindPaused},
		{130, false, ModeKeepArchive, true, "", KindPaused},
		{1, false, ModeInPlace, false, solidErr, KindSolid},
		{1, false, ModeKeepArchive, false, solidErr, KindFailed},
		{1, false, ModeInPlace, false, "error: something", KindFailed},
		{2, false, ModeInPlace, false, "", KindBadArgs},
		{3, false, ModeInPlace, false, "", KindNoSpace},
		{4, false, ModeInPlace, false, "", KindDamaged},
		{5, false, ModeInPlace, false, "", KindNeedPassword},
		{5, false, ModeInPlace, true, "", KindWrongPassword},
		{9, false, ModeInPlace, false, "", KindFailed},
		{-1, false, ModeInPlace, false, "", KindFailed},
	}
	for _, c := range cases {
		if got := classify(c.exit, c.killed, c.mode, c.pw, c.stderr); got != c.want {
			t.Errorf("classify(%d, killed=%v, mode=%v, pw=%v) = %v, want %v", c.exit, c.killed, c.mode, c.pw, got, c.want)
		}
	}
}

func TestOutputKeepsTheLastFiftyLinesAndTheFirstError(t *testing.T) {
	var o ranOut
	for i := range 80 {
		o.add(Line{Text: "line", Stderr: i%2 == 0})
	}
	o.add(Line{Text: "error: first", Stderr: true})
	o.add(Line{Text: "error: second", Stderr: true})
	o.add(Line{Text: "error: on stdout"})
	if len(o.lines) != keepLines || o.errLine != "first" {
		t.Fatalf("lines %d err %q", len(o.lines), o.errLine)
	}
}
