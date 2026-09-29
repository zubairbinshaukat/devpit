package netstat

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os/exec"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// ExecRunner is the real [Runner]: it runs the program with no shell and no
// window, and decodes what it printed from the console's OEM code page, so a
// share called "Fotos" or "写真" reads correctly.
type ExecRunner struct{}

// Run implements [Runner].
func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, int, error) {
	// #nosec G204 -- name is "net" and the arguments are built from a host
	// that ParseHost accepted; there is no shell to interpret them.
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	hideWindow(cmd)
	err := cmd.Run()
	out := decodeOEM(buf.Bytes())
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return out, 0, nil
	case errors.As(err, &exitErr):
		return out, exitErr.ExitCode(), nil
	default:
		return out, -1, err
	}
}

// WinConnector is the real [Connector], over the WNet calls.
type WinConnector struct{}

// Connect implements [Connector].
func (WinConnector) Connect(remote, user, password string) error {
	return winapi.ConnectShare(remote, user, password)
}

// Disconnect implements [Connector].
func (WinConnector) Disconnect(remote string) error { return winapi.DisconnectShare(remote, true) }

// AdapterCounters returns the [Counters] of the adapter Windows would use to
// reach host, or an error when host is not an IPv4 address or the adapter
// cannot be found. It does not send a packet.
func AdapterCounters(host string) (Counters, uint32, error) {
	ip := net.ParseIP(host)
	if ip == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, 0, errors.New("cannot resolve the host to find its adapter")
		}
		ip = ips[0].IP
	}
	idx, err := winapi.BestInterface(ip)
	if err != nil {
		return nil, 0, err
	}
	return func() (uint64, uint64, error) { return winapi.InterfaceOctets(idx) }, idx, nil
}
