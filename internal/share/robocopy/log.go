package robocopy

import (
	"bytes"
	"errors"
	"io"
	"os"
	"unicode/utf16"
)

// LogSource is where a run's log lives. The real one is a file; tests use a
// buffer they append to while the fake "robocopy" runs.
type LogSource interface {
	// ReadFrom returns every byte from offset off to the current end. A log
	// that does not exist yet is an empty read, not an error.
	ReadFrom(off int64) ([]byte, error)
}

// FileLog is a [LogSource] over a file on disk.
type FileLog struct {
	// Path is the log file robocopy writes with /UNILOG.
	Path string
}

// ReadFrom implements [LogSource].
func (f FileLog) ReadFrom(off int64) ([]byte, error) {
	fh, err := os.Open(f.Path) // #nosec G304 -- the log is a file Devpit told robocopy to write in its own cache directory
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer fh.Close() //nolint:errcheck // read-only handle
	if _, err := fh.Seek(off, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(fh)
}

// bom is the byte order mark /UNILOG starts a file with: UTF-16, little
// endian.
var bom = []byte{0xFF, 0xFE}

// Tail reads a growing log and hands back whole lines. /UNILOG writes UTF-16
// little endian, so a read can end in the middle of a two-byte character or
// of a line; Tail keeps the leftover for the next read instead of decoding
// half a character.
type Tail struct {
	src     LogSource
	off     int64
	carry   []byte
	partial []rune
	started bool
	utf16   bool
}

// NewTail starts reading src from its beginning.
func NewTail(src LogSource) *Tail { return &Tail{src: src} }

// Poll returns the lines completed since the last call.
func (t *Tail) Poll() ([]string, error) {
	b, err := t.src.ReadFrom(t.off)
	if err != nil {
		return nil, err
	}
	t.off += int64(len(b))
	data := append(t.carry, b...)
	t.carry = nil
	if !t.started {
		if len(data) < 2 {
			t.carry = data
			return nil, nil
		}
		t.started = true
		if bytes.HasPrefix(data, bom) {
			t.utf16 = true
			data = data[2:]
		}
	}
	var runes []rune
	if t.utf16 {
		if len(data)%2 == 1 {
			t.carry = data[len(data)-1:]
			data = data[:len(data)-1]
		}
		units := make([]uint16, len(data)/2)
		for i := range units {
			units[i] = uint16(data[2*i]) | uint16(data[2*i+1])<<8
		}
		// A high surrogate at the very end waits for its partner.
		if n := len(units); n > 0 && units[n-1] >= 0xD800 && units[n-1] < 0xDC00 {
			t.carry = append([]byte{data[len(data)-2], data[len(data)-1]}, t.carry...)
			units = units[:n-1]
		}
		runes = utf16.Decode(units)
	} else {
		runes = []rune(string(data))
	}
	var lines []string
	for _, r := range runes {
		if r == '\n' {
			lines = append(lines, string(t.partial))
			t.partial = t.partial[:0]
			continue
		}
		t.partial = append(t.partial, r)
	}
	return lines, nil
}

// DecodeLog returns the whole text of a finished log.
func DecodeLog(b []byte) string {
	tl := NewTail(bytesSource(b))
	lines, _ := tl.Poll()
	// A finished log may end without a newline; the last line still counts.
	if len(tl.partial) > 0 {
		lines = append(lines, string(tl.partial))
	}
	var out bytes.Buffer
	for _, l := range lines {
		out.WriteString(l)
		out.WriteByte('\n')
	}
	return out.String()
}

// bytesSource is a [LogSource] over bytes already in memory.
type bytesSource []byte

// ReadFrom implements [LogSource].
func (b bytesSource) ReadFrom(off int64) ([]byte, error) {
	if off >= int64(len(b)) {
		return nil, nil
	}
	return b[off:], nil
}
