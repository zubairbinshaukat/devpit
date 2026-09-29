package elevate

import (
	"fmt"
	"regexp"
	"strconv"
)

// pipeNamePattern is the only pipe name the worker will dial: a local pipe
// (never \\host\pipe\...), named devpit-<the TUI's PID>-<random hex>. The PID
// in it is what the worker checks the pipe's server against before it reads
// a single request; see [Serve].
var pipeNamePattern = regexp.MustCompile(`^\\\\\.\\pipe\\devpit-([1-9][0-9]{0,9})-[0-9a-f]{8,64}$`)

// pipeName builds the name [Launch] creates and passes to the worker.
func pipeName(pid int, suffix string) string {
	return fmt.Sprintf(`\\.\pipe\devpit-%d-%s`, pid, suffix)
}

// pipeServerPID returns the PID of the process that must own the pipe
// called name, or an error when name is not one [Launch] could have made.
func pipeServerPID(name string) (uint32, error) {
	m := pipeNamePattern.FindStringSubmatch(name)
	if m == nil {
		return 0, fmt.Errorf("elevate: refused: %q is not a Devpit pipe name", name)
	}
	pid, err := strconv.ParseUint(m[1], 10, 32)
	if err != nil {
		return 0, fmt.Errorf("elevate: refused: %q is not a Devpit pipe name", name)
	}
	return uint32(pid), nil
}

// startedBefore reports whether a process created at server (in 100ns ticks)
// can be the one that launched a worker created at self. A PID only names a
// process while it lives; one created after the worker is a stranger that
// was handed a recycled PID, never the TUI that started the worker.
func startedBefore(server, self int64) bool { return server <= self }
