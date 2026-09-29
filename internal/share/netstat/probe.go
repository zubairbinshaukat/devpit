package netstat

import (
	"context"
	"net"
	"time"
)

// SMBPort is the TCP port file sharing listens on.
const SMBPort = "445"

// Dialer opens a TCP connection. It is net.Dialer's DialContext in
// production and a fake in tests.
type Dialer func(ctx context.Context, network, address string) (net.Conn, error)

// Reachable reports whether host accepts a TCP connection on the file
// sharing port within timeout. It is a better test than ping, which
// Windows blocks by default.
func Reachable(ctx context.Context, dial Dialer, host string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := dial(ctx, "tcp", net.JoinHostPort(host, SMBPort))
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// Backoff is the wait before probe number attempt (from 0): 2 seconds,
// doubling to a cap of 30, so a PC that comes back is found within half a
// minute without hammering a network that is down.
func Backoff(attempt int) time.Duration {
	d := 2 * time.Second
	for range attempt {
		d *= 2
		if d >= 30*time.Second {
			return 30 * time.Second
		}
	}
	return d
}

// WaitReachable probes host until it answers or ctx ends, sleeping with
// [Backoff] between tries. onWait, if not nil, is told how long the next wait
// is, so a screen can say "trying again in 8s". sleep is time.Sleep-like and
// injected so tests need no real time.
func WaitReachable(ctx context.Context, dial Dialer, host string, sleep func(context.Context, time.Duration), onWait func(time.Duration)) error {
	for attempt := 0; ; attempt++ {
		if Reachable(ctx, dial, host, 3*time.Second) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		d := Backoff(attempt)
		if onWait != nil {
			onWait(d)
		}
		sleep(ctx, d)
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// SleepCtx is the real sleep for [WaitReachable]: it returns early when ctx
// ends.
func SleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
