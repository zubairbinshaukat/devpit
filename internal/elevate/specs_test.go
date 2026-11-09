package elevate

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/sysinfo"
)

// specsExecutor is a fake executor that also stands in for the drive read.
type specsExecutor struct {
	fakeExecutor
	mu    sync.Mutex
	reads int
	block chan struct{}
}

func (s *specsExecutor) ReadReliability(ctx context.Context) ([]sysinfo.Reliability, error) {
	s.mu.Lock()
	s.reads++
	s.mu.Unlock()
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []sysinfo.Reliability{{Disk: 0, WearPct: 4, PowerOnHours: 1234, ReadErrors: 0}}, nil
}

func (s *specsExecutor) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// The client's request is the kind and an ID, and nothing else.
func TestSpecsRequestCarriesNoArguments(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	worker, tui := net.Pipe()
	defer worker.Close()
	go func() {
		lw := newLineWriter(worker)
		_ = lw.writeJSON(Event{Event: EventHello, PID: 1, Elevated: true})
	}()
	client, err := newClient(ctx, tui)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = client.Specs(ctx) }()
	var raw map[string]any
	lr := newLineReader(worker)
	if err := lr.readJSON(&raw); err != nil {
		t.Fatal(err)
	}
	if raw["kind"] != string(KindSpecs) {
		t.Fatalf("kind = %v", raw["kind"])
	}
	for k := range raw {
		if k != "kind" && k != "id" {
			t.Errorf("a specs request carries %q", k)
		}
	}

	// The worker refuses a specs request with any argument, without reading.
	for _, req := range []Request{
		{ID: "a", Kind: KindSpecs, Argv: []string{"powershell", "-c", "x"}},
		{ID: "b", Kind: KindSpecs, Path: `C:\Windows`},
		{ID: "c", Kind: KindSpecs, Share: &ShareRequest{}},
		{ID: "d", Kind: KindSpecs, Target: "x"},
		{ID: "e", Kind: KindSpecs, TimeoutSec: 999},
	} {
		se := &specsExecutor{}
		var buf strings.Builder
		ev := handleSpecs(ctx, newLineWriter(nopCloser{&buf}), se, req)
		if ev.Err == "" || !strings.HasPrefix(ev.Err, "refused") || se.count() != 0 || buf.Len() != 0 {
			t.Errorf("%+v: err=%q reads=%d out=%q", req, ev.Err, se.count(), buf.String())
		}
	}
}

// nopCloser lets a strings.Builder stand in for the pipe.
type nopCloser struct{ *strings.Builder }

func (nopCloser) Close() error { return nil }

// Over the wire the worker runs the fixed read, never a command, and the
// counters come back intact; cancelling stops it in the worker.
func TestWorkerRunsOnlyTheFixedHealthRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	se := &specsExecutor{}
	client, _ := dial(ctx, t, se)
	rs, err := client.Specs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].WearPct != 4 || rs[0].PowerOnHours != 1234 {
		t.Fatalf("counters = %+v", rs)
	}
	if se.count() != 1 || len(se.execCalls) != 0 || len(se.removed()) != 0 {
		t.Fatalf("the worker did more than the read: reads=%d exec=%v remove=%v", se.count(), se.execCalls, se.removed())
	}

	blocked := &specsExecutor{block: make(chan struct{})}
	defer close(blocked.block)
	c2, _ := dial(ctx, t, blocked)
	short, cancelShort := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelShort()
	start := time.Now()
	if _, err := c2.Specs(short); err == nil {
		t.Fatal("a cancelled read must fail")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("cancel waited for the read")
	}
}
