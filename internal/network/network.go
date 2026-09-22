// Package network provides local/public IP lookup, ping and DNS-flush
// helpers for the Network Tools screen. It never imports Bubble Tea or any
// UI package: screens call it from a goroutine and bridge results back with
// a tea.Cmd.
//
// Every external process this package runs (ping, ipconfig) goes through
// the injectable [Runner] interface so tests never exec a real binary.
package network

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
)

// Runner runs an external command, streaming each line of its combined
// stdout+stderr to onLine as it arrives (onLine may be nil), and returns
// the full captured output. Implementations must respect ctx cancellation.
type Runner interface {
	Run(ctx context.Context, onLine func(line string), name string, args ...string) (output string, err error)
}

// Options configures the package's exported functions that shell out to an
// external process (currently [Ping] and [FlushDNS]).
type Options struct {
	// Runner runs the external command. A nil Runner uses the real
	// os/exec-backed implementation.
	Runner Runner
}

func (o Options) runner() Runner {
	if o.Runner != nil {
		return o.Runner
	}
	return execRunner{}
}

// execRunner is the real [Runner], backed by os/exec.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, onLine func(line string), name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // name/args are internal constants (ping, ipconfig), never user input.

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = cmd.Stdout // combined output, matching what the real console would show

	var all strings.Builder

	if err := cmd.Start(); err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		all.WriteString(line)
		all.WriteByte('\n')
		if onLine != nil {
			onLine(line)
		}
	}
	// bufio.Scanner.Err never returns io.EOF (it reports nil at a clean
	// EOF), so any non-nil error here is a genuine read failure; the
	// process still needs to be waited on to release its resources.
	if scanErr := scanner.Err(); scanErr != nil {
		_ = cmd.Wait()
		return all.String(), scanErr
	}

	if err := cmd.Wait(); err != nil {
		return all.String(), err
	}
	return all.String(), nil
}
