package devpit

import (
	"bytes"
	"strings"
	"testing"
)

// fakeTTY is a stream with a file descriptor, so the terminal test can be
// faked without a real console.
type fakeTTY struct {
	bytes.Buffer
	fd uintptr
}

func (f *fakeTTY) Fd() uintptr { return f.fd }

// Interactive means both stdin and stdout are terminals, and nothing else.
func TestInteractiveNeedsBothStreamsToBeTerminals(t *testing.T) {
	t.Parallel()
	const in, out = 10, 11
	terminals := func(fds ...uintptr) func(uintptr) bool {
		return func(fd uintptr) bool {
			for _, f := range fds {
				if f == fd {
					return true
				}
			}
			return false
		}
	}
	stdin, stdout := &fakeTTY{fd: in}, &fakeTTY{fd: out}

	cases := []struct {
		name string
		got  bool
		want bool
	}{
		{"both terminals", interactive(stdin, stdout, terminals(in, out)), true},
		{"stdin redirected", interactive(stdin, stdout, terminals(out)), false},
		{"stdout redirected", interactive(stdin, stdout, terminals(in)), false},
		{"neither", interactive(stdin, stdout, terminals()), false},
		{"buffers", interactive(strings.NewReader(""), &bytes.Buffer{}, terminals(in, out)), false},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: interactive = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

// The hints make output plain and nothing more; unset, empty and "false"
// values do not count.
func TestPlainOutputHints(t *testing.T) {
	t.Parallel()
	env := func(kv ...string) lookupEnv {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	cases := []struct {
		env  lookupEnv
		want bool
	}{
		{env(), false},
		{nil, false},
		{env("CI", "true"), true},
		{env("CI", "1"), true},
		{env("CI", "false"), false},
		{env("CI", "0"), false},
		{env("CI", ""), false},
		{env("CLAUDECODE", "1"), true},
		{env("NO_COLOR", "1"), true},
		{env("NO_COLOR", ""), false},
		{env("TERM", "dumb"), true},
		{env("TERM", "xterm-256color"), false},
	}
	for i, tc := range cases {
		if got := plainOutput(tc.env); got != tc.want {
			t.Errorf("case %d: plainOutput = %v, want %v", i, got, tc.want)
		}
	}
}
