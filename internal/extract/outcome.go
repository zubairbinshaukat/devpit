package extract

import "strings"

// Exit codes (section 6.6). The exit code is the verdict (rule 84): hatchzip's
// text only picks a message, here only to tell a solid RAR's exit 1 from any
// other failure, which matters because a solid RAR can still be extracted
// with the archive kept.

// solidMessage is the part of hatchzip's refusal of a solid RAR in place
// (spike X11: "error: this RAR archive is solid: …"). It is matched as a
// phrase, not the word "solid", which a file name in an error line could hold.
const solidMessage = "archive is solid"

func isSolidMessage(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), solidMessage)
}

// diskFullMessage is the part of hatchzip's "the disk is full" stop that rule
// 85 looks for after exit 1 (real run, hatchzip 0.1.10: "error: not enough
// disk space to continue: '<entry>' requires 941.7 MB and 169.7 MB is
// available (2.0 GB reserved)."). Only the phrase is matched, so a reworded
// tail still counts.
const diskFullMessage = "not enough disk space"

func isDiskFullMessage(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), diskFullMessage)
}

// mapCheck maps a check's exit code to its verdict.
func mapCheck(exit int, stderr string, passwordGiven bool, mode Mode) Verdict {
	switch exit {
	case 0:
		return VerdictFits
	case 1:
		if mode == ModeInPlace && isSolidMessage(stderr) {
			return VerdictSolid
		}
	case 2:
		return VerdictBadArgs
	case 3:
		return VerdictNoSpace
	case 4:
		return VerdictDamaged
	case 5:
		if passwordGiven {
			return VerdictWrongPassword
		}
		return VerdictNeedPassword
	}
	return VerdictFailed
}

// classify maps how a run ended to its Kind. A kill Devpit made is Stopped
// whatever exit code Go reports for it (rule 80).
func classify(exit int, killedByUs bool, mode Mode, passwordGiven bool, stderr string) Kind {
	if killedByUs {
		return KindStopped
	}
	switch exit {
	case 0:
		return KindDone
	case 130:
		return KindPaused
	case 1:
		if mode == ModeInPlace && isSolidMessage(stderr) {
			return KindSolid
		}
	case 2:
		return KindBadArgs
	case 3:
		return KindNoSpace
	case 4:
		return KindDamaged
	case 5:
		if passwordGiven {
			return KindWrongPassword
		}
		return KindNeedPassword
	}
	return KindFailed
}

// Output collection.

const keepLines = 50

// ranOut is what one run printed.
type ranOut struct {
	exit    int
	lines   []string
	stdout  []string
	stderr  []string
	errLine string
}

func (o *ranOut) add(ln Line) {
	o.lines = appendKeep(o.lines, ln.Text)
	if ln.Stderr {
		o.stderr = append(o.stderr, ln.Text)
		if o.errLine == "" {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(ln.Text), "error:"); ok {
				o.errLine = strings.TrimSpace(rest)
			}
		}
		return
	}
	o.stdout = append(o.stdout, ln.Text)
}

// appendKeep appends s and keeps only the last keepLines lines.
func appendKeep(lines []string, s ...string) []string {
	lines = append(lines, s...)
	if len(lines) > keepLines {
		lines = append([]string(nil), lines[len(lines)-keepLines:]...)
	}
	return lines
}

func (o *ranOut) stdoutText() string { return strings.Join(o.stdout, "\n") }
func (o *ranOut) stderrText() string { return strings.Join(o.stderr, "\n") }
