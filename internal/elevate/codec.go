package elevate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// maxLineSize is the largest single JSON line (request, event, or one line
// of a command's output) the codec accepts. A command that emits a single
// line longer than this is not something Devpit expects to run.
const maxLineSize = 1 << 20 // 1 MiB

// lineWriter serialises one JSON value per line to w. It is safe for
// concurrent callers because a running exec job's stdout and stderr are
// scanned on separate goroutines that both write "line" events.
type lineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newLineWriter(w io.Writer) *lineWriter {
	return &lineWriter{w: w}
}

// writeJSON marshals v and writes it as one newline-terminated line.
func (lw *lineWriter) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	lw.mu.Lock()
	defer lw.mu.Unlock()
	_, err = lw.w.Write(b)
	return err
}

// lineReader reads one JSON value per line from r.
type lineReader struct {
	sc *bufio.Scanner
}

func newLineReader(r io.Reader) *lineReader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), maxLineSize)
	return &lineReader{sc: sc}
}

// readJSON reads the next line and unmarshals it into v. It returns io.EOF
// once the underlying reader is exhausted cleanly.
func (lr *lineReader) readJSON(v any) error {
	if !lr.sc.Scan() {
		if err := lr.sc.Err(); err != nil {
			return err
		}
		return io.EOF
	}
	if err := json.Unmarshal(lr.sc.Bytes(), v); err != nil {
		return fmt.Errorf("elevate: malformed line: %w", err)
	}
	return nil
}
