package network

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const windowsPingSuccessSample = `
Pinging example.com [93.184.216.34] with 32 bytes of data:
Reply from 93.184.216.34: bytes=32 time=11ms TTL=56
Reply from 93.184.216.34: bytes=32 time=10ms TTL=56
Reply from 93.184.216.34: bytes=32 time=12ms TTL=56
Reply from 93.184.216.34: bytes=32 time=11ms TTL=56

Ping statistics for 93.184.216.34:
    Packets: Sent = 4, Received = 4, Lost = 0 (0% loss),
Approximate round trip times in milli-seconds:
    Minimum = 10ms, Maximum = 12ms, Average = 11ms
`

const windowsPingAllLossSample = `
Pinging 10.255.255.1 with 32 bytes of data:
Request timed out.
Request timed out.
Request timed out.
Request timed out.

Ping statistics for 10.255.255.1:
    Packets: Sent = 4, Received = 0, Lost = 4 (100% loss),
`

func TestParsePingSummary_Success(t *testing.T) {
	got := parsePingSummary(splitLines(windowsPingSuccessSample))

	want := PingResult{Sent: 4, Received: 4, MinMs: 10, AvgMs: 11, MaxMs: 12}
	if got.Sent != want.Sent || got.Received != want.Received ||
		got.MinMs != want.MinMs || got.AvgMs != want.AvgMs || got.MaxMs != want.MaxMs {
		t.Fatalf("parsePingSummary() = %+v, want %+v", got, want)
	}
}

func TestParsePingSummary_AllLoss(t *testing.T) {
	got := parsePingSummary(splitLines(windowsPingAllLossSample))

	if got.Sent != 4 || got.Received != 0 {
		t.Fatalf("parsePingSummary() = %+v, want Sent=4 Received=0", got)
	}
	// No "Approximate round trip times" block on 100% loss: the times stay zero.
	if got.MinMs != 0 || got.AvgMs != 0 || got.MaxMs != 0 {
		t.Fatalf("parsePingSummary() times = %+v, want all zero on total loss", got)
	}
}

func TestParsePingSummary_Empty(t *testing.T) {
	got := parsePingSummary(nil)
	if got.Sent != 0 || got.Received != 0 || got.MinMs != 0 || got.AvgMs != 0 || got.MaxMs != 0 || got.Raw != nil {
		t.Fatalf("parsePingSummary(nil) = %+v, want zero value", got)
	}
}

// fakeRunner is a [Runner] test double that never execs a real process.
type fakeRunner struct {
	output string
	err    error

	calledName string
	calledArgs []string
}

func (f *fakeRunner) Run(_ context.Context, onLine func(string), name string, args ...string) (string, error) {
	f.calledName = name
	f.calledArgs = args
	if onLine != nil {
		for _, line := range splitLines(f.output) {
			onLine(line)
		}
	}
	return f.output, f.err
}

func TestPing_UsesInjectedRunnerNotRealBinary(t *testing.T) {
	fr := &fakeRunner{output: windowsPingSuccessSample}

	var streamed []string
	result, err := Ping(context.Background(), "example.com", 4, func(l string) {
		streamed = append(streamed, l)
	}, Options{Runner: fr})
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}

	if fr.calledName != "ping" {
		t.Fatalf("runner invoked with name %q, want %q", fr.calledName, "ping")
	}
	if fr.calledArgs[len(fr.calledArgs)-1] != "example.com" {
		t.Fatalf("runner args = %v, want host as last arg", fr.calledArgs)
	}
	if result.Sent != 4 || result.Received != 4 {
		t.Fatalf("Ping() result = %+v", result)
	}
	if len(streamed) == 0 {
		t.Fatal("onLine callback was never invoked")
	}
}

func TestPing_AllLossIsNotAnError(t *testing.T) {
	// A real `ping` exits non-zero on total loss; Devpit should still treat
	// a parseable all-loss summary as a successful result, not an error.
	fr := &fakeRunner{output: windowsPingAllLossSample, err: errors.New("exit status 1")}

	result, err := Ping(context.Background(), "10.255.255.1", 4, nil, Options{Runner: fr})
	if err != nil {
		t.Fatalf("Ping() error = %v, want nil for a parseable all-loss result", err)
	}
	if result.Sent != 4 || result.Received != 0 {
		t.Fatalf("Ping() result = %+v", result)
	}
}

func TestPing_RealFailurePropagates(t *testing.T) {
	fr := &fakeRunner{output: "", err: errors.New("exec: \"ping\": executable file not found in $PATH")}

	_, err := Ping(context.Background(), "example.com", 4, nil, Options{Runner: fr})
	if err == nil {
		t.Fatal("Ping() error = nil, want an error when no summary could be parsed")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Ping() error = %v", err)
	}
}
