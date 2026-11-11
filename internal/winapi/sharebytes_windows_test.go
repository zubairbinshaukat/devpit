//go:build windows

package winapi

import (
	"errors"
	"testing"
)

// TestTheSMBShareCountersOpenByTheirEnglishNames is read-only: it opens both
// per-share counters on this PC, lists their instances, and reads a share
// that does not exist. It skips when the SMB counters are not installed.
func TestTheSMBShareCountersOpenByTheirEnglishNames(t *testing.T) {
	cases := []struct {
		name string
		open func() (*ShareBytes, error)
	}{
		{"client", func() (*ShareBytes, error) { return OpenClientShareBytes(`\\192.0.2.1\devpit-no-such-share`) }},
		{"server", func() (*ShareBytes, error) { return OpenServerShareBytes("devpit-no-such-share") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sb, err := c.open()
			if missingCounter(err) {
				t.Skipf("the SMB counters are not installed here: %v", err)
			}
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer sb.Close()
			names, err := sb.instances()
			if err != nil && !errors.Is(err, ErrNoShareInstance) {
				t.Fatalf("listing instances: %v", err)
			}
			t.Logf("instances: %q", names)
			if c.name == "client" {
				// Every real instance opens by its own UNC path.
				for _, n := range names {
					if clientInstanceKey(n) == "" {
						continue
					}
					inst, err := OpenClientShareBytes(`\` + n)
					if err != nil {
						t.Fatalf("open %q: %v", n, err)
					}
					v, err := inst.Read()
					inst.Close()
					if err != nil {
						t.Errorf("reading %q by its UNC path: %v", n, err)
					}
					t.Logf("%s: %d bytes", n, v)
				}
			}
			if _, err := sb.Read(); !errors.Is(err, ErrNoShareInstance) {
				t.Errorf("reading a share that does not exist = %v, want ErrNoShareInstance", err)
			}
			sb.Close()
			sb.Close() // twice is harmless
			if _, err := sb.Read(); err == nil {
				t.Error("a closed counter still reads")
			}
		})
	}
}

// TestTheTotalInstanceReadsAsACount reads _Total of the client counter, which
// is there whenever the counter set is, twice: the raw value is a running
// count, so it never goes down.
func TestTheTotalInstanceReadsAsACount(t *testing.T) {
	sb, err := openShareBytes(ClientShareReadBytesPath, func(s string) bool { return s == "_Total" })
	if missingCounter(err) {
		t.Skipf("the SMB counters are not installed here: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close()
	a, err := sb.Read()
	if errors.Is(err, ErrNoShareInstance) {
		t.Skip("no _Total instance on this PC")
	}
	if err != nil {
		t.Fatal(err)
	}
	b, err := sb.Read()
	if err != nil {
		t.Fatal(err)
	}
	if b < a {
		t.Errorf("the count went down: %d then %d", a, b)
	}
}
