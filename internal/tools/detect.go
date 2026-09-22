// Package tools detects developer tools installed on the machine and drives
// the package managers that install and update them.
//
// Detection never runs at package init and never runs at all until something
// asks for it: [Detector.Get] and [Detector.All] look a tool up lazily, run
// its "--version" (or equivalent) probe with a short timeout, and memoize the
// result for the lifetime of the Detector. [Detector.All] fans the lookups
// out across a bounded pool of goroutines so a cold "detect everything" call
// costs roughly one probe's wall time, not fifteen.
//
// Nothing here execs a real package manager or touches the network; that is
// [managers.Manager]'s job, driven by the install and update screens.
package tools

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"sync"
	"time"
)

// Tool is the result of probing for one developer tool on PATH.
type Tool struct {
	// Name is the short, stable identifier Devpit uses for the tool (for
	// example "node"), not necessarily the executable's own name.
	Name string
	// Exe is the executable name that was looked up on PATH.
	Exe string
	// Path is the resolved absolute path, set only when Found is true.
	Path string
	// Version is a best-effort, leniently parsed version string. It may be
	// empty even when Found is true, if the tool printed nothing usable.
	Version string
	// Found reports whether the executable was located on PATH at all.
	Found bool
}

// probeTimeout bounds each tool's version probe so one hung process never
// stalls the whole detection pass.
const probeTimeout = 3 * time.Second

// defaultPoolSize bounds how many probes [Detector.All] runs at once.
const defaultPoolSize = 8

// LookPathFunc resolves an executable name to an absolute path, matching the
// signature of [exec.LookPath]. Tests inject a fake so detection never
// touches the real PATH.
type LookPathFunc func(file string) (string, error)

// RunFunc runs a resolved executable with arguments and returns its combined
// output. Tests inject a fake so detection never execs a real binary.
type RunFunc func(ctx context.Context, path string, args ...string) (string, error)

// toolSpec is the fixed, built-in description of how to detect one tool.
type toolSpec struct {
	name        string
	exe         string
	versionArgs []string
}

// specs lists every tool Devpit knows how to detect, in the order
// [Detector.All] reports them.
var specs = []toolSpec{
	{name: "winget", exe: "winget", versionArgs: []string{"--version"}},
	{name: "scoop", exe: "scoop", versionArgs: []string{"--version"}},
	{name: "choco", exe: "choco", versionArgs: []string{"--version"}},
	{name: "docker", exe: "docker", versionArgs: []string{"--version"}},
	{name: "node", exe: "node", versionArgs: []string{"--version"}},
	{name: "npm", exe: "npm", versionArgs: []string{"--version"}},
	{name: "pnpm", exe: "pnpm", versionArgs: []string{"--version"}},
	{name: "yarn", exe: "yarn", versionArgs: []string{"--version"}},
	{name: "pip", exe: "pip", versionArgs: []string{"--version"}},
	{name: "python", exe: "python", versionArgs: []string{"--version"}},
	{name: "cargo", exe: "cargo", versionArgs: []string{"--version"}},
	{name: "go", exe: "go", versionArgs: []string{"version"}},
	{name: "git", exe: "git", versionArgs: []string{"--version"}},
	{name: "code", exe: "code", versionArgs: []string{"--version"}},
	{name: "wt", exe: "wt", versionArgs: []string{"--version"}},
}

// Names returns the stable, ordered list of tool names Devpit detects.
func Names() []string {
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.name
	}
	return names
}

// entry holds the memoized state for one tool. Once a Detector is built, the
// entries map is never mutated again, so concurrent reads of the map itself
// are safe; each entry's own sync.Once guards its tool field.
type entry struct {
	spec toolSpec
	once sync.Once
	tool Tool
}

// Option configures a [Detector].
type Option func(*Detector)

// WithLookPath overrides how executables are resolved on PATH. Tests use
// this to avoid touching the real filesystem.
func WithLookPath(f LookPathFunc) Option {
	return func(d *Detector) { d.lookPath = f }
}

// WithRun overrides how a resolved executable is invoked. Tests use this to
// avoid execing real binaries.
func WithRun(f RunFunc) Option {
	return func(d *Detector) { d.run = f }
}

// WithPoolSize bounds how many probes [Detector.All] runs concurrently. n
// less than 1 is ignored.
func WithPoolSize(n int) Option {
	return func(d *Detector) {
		if n > 0 {
			d.poolSize = n
		}
	}
}

// Detector looks up and memoizes developer tools. A zero-value Detector is
// not usable; construct one with [New].
type Detector struct {
	lookPath LookPathFunc
	run      RunFunc
	poolSize int
	entries  map[string]*entry
}

// New builds a Detector for the fixed tool list in [Names]. Detection does
// not start until [Detector.Get] or [Detector.All] is called.
func New(opts ...Option) *Detector {
	d := &Detector{
		lookPath: exec.LookPath,
		run:      runCombinedOutput,
		poolSize: defaultPoolSize,
	}
	for _, opt := range opts {
		opt(d)
	}
	d.entries = make(map[string]*entry, len(specs))
	for _, s := range specs {
		d.entries[s.name] = &entry{spec: s}
	}
	return d
}

// Get returns the detection result for name, running the probe on first
// call and returning the memoized result on every call after that. An
// unknown name returns a zero Tool with Name set and Found false.
func (d *Detector) Get(ctx context.Context, name string) Tool {
	e, ok := d.entries[name]
	if !ok {
		return Tool{Name: name}
	}
	e.once.Do(func() {
		e.tool = d.detectOne(ctx, e.spec)
	})
	return e.tool
}

// All detects every known tool, fanning the probes out across a bounded
// pool of goroutines. Results are returned in the fixed order of [Names],
// not completion order. Tools already memoized by a prior Get or All call
// return instantly.
func (d *Detector) All(ctx context.Context) []Tool {
	results := make([]Tool, len(specs))
	sem := make(chan struct{}, d.poolSize)
	var wg sync.WaitGroup
	for i, s := range specs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, name string) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = d.Get(ctx, name)
		}(i, s.name)
	}
	wg.Wait()
	return results
}

// detectOne runs the actual LookPath-plus-version probe for one tool spec.
func (d *Detector) detectOne(ctx context.Context, spec toolSpec) Tool {
	tool := Tool{Name: spec.name, Exe: spec.exe}

	path, err := d.lookPath(spec.exe)
	if err != nil {
		return tool
	}
	tool.Path = path
	tool.Found = true

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	out, err := d.run(probeCtx, path, spec.versionArgs...)
	if err != nil {
		// The executable exists; a failed or timed-out version probe just
		// means we could not learn the version string.
		return tool
	}
	tool.Version = parseVersion(out)
	return tool
}

// versionPattern picks out the first dotted version number in free-form
// tool output, for example "git version 2.43.0.windows.1" -> "2.43.0".
var versionPattern = regexp.MustCompile(`\d+(\.\d+){1,3}(-[0-9A-Za-z.]+)?`)

// parseVersion leniently extracts a version number from a tool's raw
// "--version" output. It returns the empty string if none is found.
func parseVersion(out string) string {
	return versionPattern.FindString(out)
}

// runCombinedOutput is the default [RunFunc]: it runs path with args and
// returns combined stdout+stderr, trimmed of nothing so callers can parse
// leniently.
func runCombinedOutput(ctx context.Context, path string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}
