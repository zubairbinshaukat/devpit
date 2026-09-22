package fonts

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// download fetches url and returns its full body. Only failures that mean
// "this machine cannot reach the network at all" (DNS resolution, dial
// refused/unreachable) are wrapped in *OfflineError so callers can skip the
// install silently; every other failure — a non-2xx status, a mid-transfer
// reset, a TLS error — surfaces as its own specific error so it is never
// mistaken for "no internet" (see isOffline).
func download(ctx context.Context, client *http.Client, url string, progress Progress) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("fonts: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		if isOffline(err) {
			return nil, &OfflineError{Err: err}
		}
		return nil, fmt.Errorf("fonts: download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		host := req.URL.Host
		if resp.Request != nil && resp.Request.URL != nil {
			host = resp.Request.URL.Host // the final URL after redirects
		}
		return nil, fmt.Errorf("fonts: download failed: HTTP %d from %s", resp.StatusCode, host)
	}

	total := resp.ContentLength
	var buf bytes.Buffer
	if total > 0 {
		buf.Grow(int(total))
	}
	var done int64
	chunk := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
			done += int64(n)
			if progress != nil {
				progress("download", done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if isOffline(rerr) {
				return nil, &OfflineError{Err: rerr}
			}
			return nil, fmt.Errorf("fonts: read response body: %w", rerr)
		}
	}
	return buf.Bytes(), nil
}

// isOffline reports whether err means this machine cannot reach the
// network at all: DNS resolution failed, or the transport never managed to
// open a connection (refused, unreachable, no route). It deliberately does
// NOT match on net.Error / Timeout() / Temporary() in general: every error
// http.Client.Do returns is wrapped in *url.Error, which itself implements
// net.Error, so a broad net.Error check matches almost anything — a 403, a
// TLS failure, a connection reset mid-download — and misreports it as "no
// internet" even on a machine with a perfectly good connection. Only a
// failure during the dial phase (net.OpError.Op == "dial") or a bare DNS
// error counts as offline; anything after a connection was established
// (reads, writes, TLS renegotiation, HTTP status) surfaces as itself.
func isOffline(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	return false
}

// verifySHA256 fails closed: data must hash to want, compared
// case-insensitively.
func verifySHA256(data []byte, want string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("fonts: checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}

// extractFont reads the single file named name out of a zip archive held
// in zipData, matching on base name so it works regardless of which
// directory the release puts it in. It never writes anything the archive
// doesn't explicitly contain under that name; every other entry is
// ignored.
func extractFont(zipData []byte, name string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("fonts: open archive: %w", err)
	}
	for _, f := range r.File {
		if path.Base(filepath.ToSlash(f.Name)) != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("fonts: open %s in archive: %w", name, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return nil, fmt.Errorf("fonts: read %s from archive: %w", name, err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("fonts: %s not found in archive", name)
}

// writeFileAtomic writes data to a temp file in filepath.Dir(dest) and
// renames it over dest, so a crash or a concurrent reader never observes a
// partially written font file.
func writeFileAtomic(dest string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".devpit-font-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}
