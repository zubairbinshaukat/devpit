package launch

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// TargetKind says how a program found on PATH is really started.
type TargetKind string

const (
	// KindProgram: an .exe or .com, started directly.
	KindProgram TargetKind = "program"
	// KindNodeScript: an npm cmd-shim, started as node.exe <script>.
	KindNodeScript TargetKind = "node script"
	// KindBatch: any other .cmd or .bat, started through cmd.exe with
	// strictly checked arguments.
	KindBatch TargetKind = "batch"
)

// Target is what actually gets started for a path [Find] returned.
type Target struct {
	Kind TargetKind
	// Path is the program started: the tool's .exe, node.exe, or the
	// script itself for KindBatch.
	Path string
	// Prefix comes before the person's arguments: the node script.
	Prefix []string
	// Script is the .cmd the target came from, for messages.
	Script string
}

// Prepare works out how to start path. A .cmd or .bat is read to see
// whether it is an npm cmd-shim; if it is, node.exe (next to the script, as
// the shim itself prefers, else from PATH through find) runs the script
// directly.
func Prepare(path string, find FindOptions) (Target, error) {
	if !IsBatch(path) {
		return Target{Kind: KindProgram, Path: path}, nil
	}
	t := Target{Kind: KindBatch, Path: path, Script: path}
	data, err := readSmall(path, 64<<10)
	if err != nil {
		return Target{}, fmt.Errorf("reading %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	prefix, ok := parseNpmShim(data, dir)
	if !ok {
		return t, nil
	}
	script := prefix[len(prefix)-1]
	if fi, err := os.Stat(script); err != nil || fi.IsDir() {
		return t, nil //nolint:nilerr // no script: not an npm shim after all, so it runs as any other batch file
	}
	node := filepath.Join(dir, "node.exe")
	if fi, err := os.Stat(node); err != nil || fi.IsDir() {
		node, err = Find("node", find)
		if err != nil {
			return Target{}, fmt.Errorf("%s needs Node.js, and node was not found on PATH: %w", filepath.Base(path), err)
		}
	}
	return Target{Kind: KindNodeScript, Path: node, Prefix: prefix, Script: path}, nil
}

// Command builds the exec.Cmd that starts the target with args. For
// KindBatch it fails with an error wrapping ErrUnsafeArgument, and starts
// nothing, when an argument cannot be passed to a script safely.
func (t Target) Command(ctx context.Context, args []string) (*exec.Cmd, error) {
	switch t.Kind {
	case KindBatch:
		return batchCommand(ctx, t.Path, args)
	case KindNodeScript:
		all := append(append([]string(nil), t.Prefix...), args...)
		return exec.CommandContext(ctx, t.Path, all...), nil // #nosec G204 -- the tool the person asked for
	}
	return exec.CommandContext(ctx, t.Path, args...), nil // #nosec G204 -- the tool the person asked for
}

// Command is Prepare plus Target.Command, finding node on the process PATH.
// Adapters use it for every tool command, so an npm-installed tool is as
// safe to call as a native one.
func Command(ctx context.Context, path string, args ...string) (*exec.Cmd, error) {
	t, err := Prepare(path, FindOptions{})
	if err != nil {
		return nil, err
	}
	return t.Command(ctx, args)
}

// Spec is one program to start with [Run].
type Spec struct {
	// Path is the program, normally from [Find].
	Path string
	// Args are the arguments, without the program name.
	Args []string
	// Env is the child's whole environment; nil inherits the launcher's.
	Env []string
	// Dir is the working folder; "" keeps the launcher's.
	Dir string
	// Find is used to look for node.exe when Path is an npm cmd-shim.
	Find FindOptions
	// NoJob leaves the child out of a job object. See Run.
	NoJob bool
	// Stdin, Stdout and Stderr default to the launcher's own, so the child
	// shares the console.
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

func (s Spec) command() (*exec.Cmd, error) {
	t, err := Prepare(s.Path, s.Find)
	if err != nil {
		return nil, err
	}
	c, err := t.Command(context.Background(), s.Args)
	if err != nil {
		return nil, err
	}
	c.Env, c.Dir = s.Env, s.Dir
	c.Stdin, c.Stdout, c.Stderr = s.Stdin, s.Stdout, s.Stderr
	if c.Stdin == nil {
		c.Stdin = os.Stdin
	}
	if c.Stdout == nil {
		c.Stdout = os.Stdout
	}
	if c.Stderr == nil {
		c.Stderr = os.Stderr
	}
	return c, nil
}

func readSmall(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- a program found on PATH
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only
	return io.ReadAll(io.LimitReader(f, limit))
}
