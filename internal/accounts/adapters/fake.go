package adapters

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// FakeRunner is a Runner for tests: it answers from a table and records
// every call. It applies CheckArgs like the real runner, so a test can prove
// a forbidden command is never even attempted. It lives in a normal file so
// every adapter's tests (and the screens') can use it.
type FakeRunner struct {
	mu sync.Mutex
	// Responses maps Key(name, args...) to a canned answer.
	Responses map[string]FakeResponse
	// Func, when set, answers every call not in Responses.
	Func func(Cmd) (Result, error)
	// Missing lists program names that are "not installed".
	Missing map[string]bool
	calls   []Cmd
}

// FakeResponse is one canned answer.
type FakeResponse struct {
	Stdout, Stderr string
	Exit           int
	Err            error
	// Do runs before the answer is returned (to create a file a sign-in
	// would create, say).
	Do func(Cmd)
}

// Key is how FakeRunner looks a call up: the program and its arguments.
func Key(name string, args ...string) string {
	return strings.Join(append([]string{name}, args...), " ")
}

// Set registers an answer.
func (f *FakeRunner) Set(r FakeResponse, name string, args ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Responses == nil {
		f.Responses = map[string]FakeResponse{}
	}
	f.Responses[Key(name, args...)] = r
}

// Calls returns every call made so far.
func (f *FakeRunner) Calls() []Cmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Cmd(nil), f.calls...)
}

// Run answers a call.
func (f *FakeRunner) Run(ctx context.Context, c Cmd) (Result, error) {
	if err := CheckArgs(c.Name, c.Args); err != nil {
		return Result{}, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	missing := f.Missing[c.Name]
	r, ok := f.Responses[Key(c.Name, c.Args...)]
	fn := f.Func
	f.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if missing {
		return Result{}, fmt.Errorf("%s: %w", c.Name, accounts.ErrNotFoundTool)
	}
	if !ok {
		if fn != nil {
			return fn(c)
		}
		return Result{}, fmt.Errorf("FakeRunner: no answer for %q", Key(c.Name, c.Args...))
	}
	if r.Do != nil {
		r.Do(c)
	}
	path := `C:\fake\` + c.Name + ".exe"
	if c.Stdout != nil && r.Stdout != "" {
		_, _ = c.Stdout.Write([]byte(r.Stdout))
	}
	return Result{Stdout: []byte(r.Stdout), Stderr: []byte(r.Stderr), ExitCode: r.Exit, Path: path}, r.Err
}

// EnvOf returns the value a call set for key, and whether it unset it.
func EnvOf(c Cmd, key string) (value string, set, unset bool) {
	for _, kv := range c.Env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			value, set = v, true
		}
	}
	for _, k := range c.Unset {
		if strings.EqualFold(k, key) {
			unset = true
		}
	}
	return value, set, unset
}
