package extract

import (
	"bufio"
	"io"
	"strings"
	"sync"

	"github.com/zubairbinshaukat/devpit/internal/tools"
)

// The real runner (section 6.3) is split: what every platform shares is
// here (the child's environment, reading both pipes into Lines), the process
// start is runner_windows.go, and other platforms refuse in runner_other.go.

// passwordEnv is how hatchzip takes a password without a command line.
const passwordEnv = "HATCHZIP_PASSWORD"

// childEnv is hatchzip's environment: environ with every inherited
// HATCHZIP_PASSWORD dropped (any case), plus the request's password when one
// is set. It is the only place a password leaves its Secret (rule 79), and it
// never touches Devpit's own environment (no os.Setenv).
func childEnv(environ []string, pw Secret) []string {
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(k, passwordEnv) {
			continue
		}
		out = append(out, kv)
	}
	if !pw.Empty() {
		out = append(out, passwordEnv+"="+pw.v)
	}
	return out
}

// maxLine bounds one output line, so a run that prints a huge blob without a
// newline cannot grow memory without limit.
const maxLine = 4096

// pump reads r line by line (splitting on \n and \r, ANSI stripped, empty
// lines dropped) and sends each line to out.
func pump(r io.Reader, stderr bool, out chan<- Line) {
	br := bufio.NewReaderSize(r, 64*1024)
	var cur []byte
	flush := func() {
		if len(cur) == 0 {
			return
		}
		// CleanLine trims; the plan's detail lines are indented, so the
		// indentation is put back (piped output has no escape codes before it).
		raw := string(cur)
		if t := tools.CleanLine(raw); t != "" {
			indent := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
			out <- Line{Text: indent + t, Stderr: stderr}
		}
		cur = cur[:0]
	}
	for {
		b, err := br.ReadByte()
		if err != nil {
			flush()
			return
		}
		switch b {
		case '\n', '\r':
			flush()
		default:
			if len(cur) < maxLine {
				cur = append(cur, b)
			}
		}
	}
}

// pumpAll drains both pipes into one channel, closed when both are done;
// done is closed right after it.
func pumpAll(stdout, stderr io.Reader) (lines <-chan Line, done <-chan struct{}) {
	ch := make(chan Line, 256)
	fin := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); pump(stdout, false, ch) }()
	go func() { defer wg.Done(); pump(stderr, true, ch) }()
	go func() { wg.Wait(); close(ch); close(fin) }()
	return ch, fin
}
