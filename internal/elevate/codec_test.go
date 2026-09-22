package elevate

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestCodecRoundTripsRequestsAndEvents(t *testing.T) {
	var buf bytes.Buffer
	lw := newLineWriter(&buf)

	want := []Request{
		{ID: "a1", Kind: KindExec, Argv: []string{"choco", "upgrade", "all", "-y"}, TimeoutSec: 600},
		{Kind: KindShutdown},
		{ID: "b2", Kind: KindRemove, Path: `C:\Windows\Temp\leftover`},
	}
	for _, r := range want {
		if err := lw.writeJSON(r); err != nil {
			t.Fatalf("writeJSON: %v", err)
		}
	}

	lr := newLineReader(&buf)
	for i, w := range want {
		var got Request
		if err := lr.readJSON(&got); err != nil {
			t.Fatalf("readJSON[%d]: %v", i, err)
		}
		if got.ID != w.ID || got.Kind != w.Kind || got.Path != w.Path || got.TimeoutSec != w.TimeoutSec || !equalArgv(got.Argv, w.Argv) {
			t.Fatalf("readJSON[%d] = %+v, want %+v", i, got, w)
		}
	}
	var trailing Request
	if err := lr.readJSON(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF after the last line, got %v", err)
	}
}

func equalArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCodecEncodesOneLinePerValue(t *testing.T) {
	var buf bytes.Buffer
	lw := newLineWriter(&buf)
	events := []Event{
		{Event: EventHello, PID: 4242, Elevated: true},
		{ID: "a1", Event: EventLine, Stream: "stdout", Text: "hello"},
		{ID: "a1", Event: EventDone, ExitCode: 0},
	}
	for _, e := range events {
		if err := lw.writeJSON(e); err != nil {
			t.Fatalf("writeJSON: %v", err)
		}
	}

	lines := bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n"))
	if len(lines) != len(events) {
		t.Fatalf("got %d lines, want %d: %q", len(lines), len(events), buf.String())
	}

	lr := newLineReader(bytes.NewReader(buf.Bytes()))
	for i, want := range events {
		var got Event
		if err := lr.readJSON(&got); err != nil {
			t.Fatalf("readJSON[%d]: %v", i, err)
		}
		if got != want {
			t.Fatalf("readJSON[%d] = %+v, want %+v", i, got, want)
		}
	}
}

func TestCodecReadJSONRejectsMalformedLine(t *testing.T) {
	lr := newLineReader(bytes.NewReader([]byte("not json\n")))
	var got Event
	if err := lr.readJSON(&got); err == nil {
		t.Fatal("expected an error for a malformed line, got nil")
	}
}

func TestCodecReadJSONOnEmptyReaderIsEOF(t *testing.T) {
	lr := newLineReader(bytes.NewReader(nil))
	var got Event
	if err := lr.readJSON(&got); !errors.Is(err, io.EOF) {
		t.Fatalf("got %v, want io.EOF", err)
	}
}
