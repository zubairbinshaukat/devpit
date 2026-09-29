// Package robocopy builds robocopy command lines and reads what robocopy
// writes, without ever depending on the Windows language.
//
// Robocopy translates its whole output: the words "New File", "ERROR" and the
// summary labels are different on a German, French, Japanese or Turkish PC.
// So nothing here looks for a word. A line is recognised by its shape:
// tab-separated columns with a number before an absolute path, or a date, a
// time, a word, and a number followed by its hex form in brackets. The
// summary is the last three rows that hold six numbers. The result is the
// exit code, which is a bitmask that never changes.
//
// The package has no Bubble Tea import and never starts a process itself:
// [Runner] is the seam, so every test runs on fake output.
package robocopy

import (
	"strconv"
	"strings"
)

// Threads is how many files robocopy copies at once. 16 keeps a gigabit link
// full even with many small files; the documented default is 8.
const Threads = 16

// Retries and WaitSeconds are robocopy's own retry of a file that failed:
// three more tries, five seconds apart. The default (a million tries, 30
// seconds apart) would look like a hang.
const (
	Retries     = 3
	WaitSeconds = 5
)

// trimSlash removes trailing backslashes from a path argument.
//
// Robocopy reads a backslash before a closing quote as an escaped quote, so
// "D:\Games\" reaches it as D:\Games" and the rest of the command line is
// eaten. A path with no trailing backslash is always safe.
func trimSlash(p string) string {
	if len(p) > 3 {
		return strings.TrimRight(p, `\/`)
	}
	return p
}

// common are the flags every run shares, each one checked against the
// robocopy documentation:
//
//	/E      copy subfolders, including empty ones
//	/XJ     skip junction points, which can loop forever
//	/NP     no per-file percentage (it is ignored or noisy in multi-thread mode)
//	/BYTES  sizes as plain numbers, not "1.2 g"
//	/NJH    no job header
//	/NDL    no directory lines
//	/FP     full path on every file line, so a line is self-contained
//	/NC     no file class words, which are translated
//	/XO     never replace a destination file with an older one. Robocopy's
//	        default copies "Older" files too, so a save or a document edited
//	        at the destination after an earlier copy was silently overwritten
//	        by the stale version. A half-copied file still resumes: robocopy
//	        dates it 1980-01-01, which is older than any source.
//
// There is never /MIR, /PURGE or /MOV: nothing at the destination is ever
// deleted, and nothing at the source is ever touched.
var common = []string{"/E", "/XJ", "/XO", "/NP", "/BYTES", "/NJH", "/NDL", "/FP", "/NC"}

// DryRunArgs is the command line that lists what a copy would do and copies
// nothing. /L is "list only". The output goes to a Unicode log file, not the
// console, because the console is in the OEM code page and would mangle
// every file name that is not plain ASCII.
func DryRunArgs(src, dst, logPath string) []string {
	args := []string{trimSlash(src), trimSlash(dst), "/L"}
	args = append(args, common...)
	// One try, no wait: a dry run must not sit retrying an unreadable file.
	args = append(args, "/R:0", "/W:0", "/UNILOG:"+logPath)
	return args
}

// CopyArgs is the command line of the real copy.
//
//	/MT:16     sixteen files at once
//	/Z         restartable mode: a half-copied file carries on, not restarts
//	/R:3 /W:5  three retries, five seconds apart
//
// Finished files are skipped on every later run because robocopy sees the
// same size and time on both sides, which is what makes Resume work with no
// state of its own. The log is Unicode and written fresh for each run.
func CopyArgs(src, dst, logPath string) []string {
	args := []string{trimSlash(src), trimSlash(dst)}
	args = append(args, common...)
	args = append(args,
		"/MT:"+strconv.Itoa(Threads),
		"/Z",
		"/R:"+strconv.Itoa(Retries),
		"/W:"+strconv.Itoa(WaitSeconds),
		"/UNILOG:"+logPath,
	)
	return args
}

// LongPath returns p in the \\?\ form that lifts Windows' 260 character
// limit, for a local path. A path that already has the prefix, a UNC path and
// a short path are returned as they are, except that a UNC path longer than
// the limit gets the \\?\UNC\ form.
func LongPath(p string) string {
	if strings.HasPrefix(p, `\\?\`) || len(p) < 240 {
		return p
	}
	if strings.HasPrefix(p, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(p, `\\`)
	}
	return `\\?\` + p
}
