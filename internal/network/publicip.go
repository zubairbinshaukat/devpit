package network

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// publicIPTimeout bounds every attempt at reaching the public-IP services,
// so a caller running this in a goroutine is never left hanging.
const publicIPTimeout = 5 * time.Second

// publicIPServices is tried in order; the first that answers wins.
var publicIPServices = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
}

// OfflineError reports that no public-IP service could be reached, most
// likely because the machine has no internet access.
type OfflineError struct {
	Err error
}

func (e *OfflineError) Error() string {
	return fmt.Sprintf("network: could not reach a public IP service: %v", e.Err)
}

func (e *OfflineError) Unwrap() error { return e.Err }

// PublicIP asks api.ipify.org for the machine's public IP, falling back to
// ifconfig.me/ip if that fails. Each attempt is bounded to five seconds.
// If client is nil, http.DefaultClient is used. Callers run this in a
// goroutine: it never blocks the UI, and an unreachable network surfaces as
// a typed *OfflineError rather than a generic one.
func PublicIP(ctx context.Context, client *http.Client) (net.IP, error) {
	if client == nil {
		client = http.DefaultClient
	}

	ctx, cancel := context.WithTimeout(ctx, publicIPTimeout)
	defer cancel()

	var lastErr error
	for _, url := range publicIPServices {
		ip, err := fetchIP(ctx, client, url)
		if err == nil {
			return ip, nil
		}
		lastErr = err
	}
	return nil, &OfflineError{Err: lastErr}
}

func fetchIP(ctx context.Context, client *http.Client, url string) (net.IP, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("network: %s returned %s", url, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return nil, err
	}

	text := strings.TrimSpace(string(body))
	ip := net.ParseIP(text)
	if ip == nil {
		return nil, fmt.Errorf("network: %s returned an unparseable address %q", url, text)
	}
	return ip, nil
}
