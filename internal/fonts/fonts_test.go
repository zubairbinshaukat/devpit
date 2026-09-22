package fonts

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- fakes -------------------------------------------------------------

type fakeRegistry struct {
	values map[string]string
	setErr error
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{values: map[string]string{}}
}

func (f *fakeRegistry) Set(name, value string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.values[name] = value
	return nil
}

func (f *fakeRegistry) Delete(name string) error {
	delete(f.values, name)
	return nil
}

func (f *fakeRegistry) Get(name string) (string, bool, error) {
	v, ok := f.values[name]
	return v, ok, nil
}

type fakeGDI struct {
	added, removed []string
	broadcasts     int
	addErr         error
}

func (f *fakeGDI) AddFontResource(path string) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.added = append(f.added, path)
	return nil
}

func (f *fakeGDI) RemoveFontResource(path string) error {
	f.removed = append(f.removed, path)
	return nil
}

func (f *fakeGDI) BroadcastFontChange() { f.broadcasts++ }

// buildZip returns a zip archive (as bytes) containing name -> content plus
// a handful of unrelated entries, mimicking the real release asset which
// ships hundreds of unrelated font files alongside the one Devpit wants.
func buildZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, other := range []string{"README.md", "SymbolsNerdFont-Regular.ttf", "LICENSE"} {
		f, err := w.Create(other)
		if err != nil {
			t.Fatalf("create %s: %v", other, err)
		}
		if _, err := f.Write([]byte("not the font")); err != nil {
			t.Fatalf("write %s: %v", other, err)
		}
	}
	f, err := w.Create(name)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// --- pure helpers --------------------------------------------------------

func TestVerifySHA256_Mismatch(t *testing.T) {
	data := []byte("hello world")
	sum := sha256.Sum256([]byte("something else"))
	if err := verifySHA256(data, hex.EncodeToString(sum[:])); err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
}

func TestVerifySHA256_Match(t *testing.T) {
	data := []byte("hello world")
	sum := sha256.Sum256(data)
	if err := verifySHA256(data, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
	// Case-insensitive.
	if err := verifySHA256(data, hex.EncodeToString(sum[:])+""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractFont_PicksOnlyTarget(t *testing.T) {
	want := []byte("fake ttf bytes")
	archive := buildZip(t, TargetFile, want)

	got, err := extractFont(archive, TargetFile)
	if err != nil {
		t.Fatalf("extractFont: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("extractFont returned %q, want %q", got, want)
	}
}

func TestExtractFont_NotFound(t *testing.T) {
	archive := buildZip(t, "SomeOtherFile.ttf", []byte("x"))
	if _, err := extractFont(archive, TargetFile); err == nil {
		t.Fatal("expected error when target is absent from the archive")
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "font.ttf")
	if err := writeFileAtomic(dest, []byte("data"), 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "data" {
		t.Fatalf("got %q, want %q", got, "data")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly the destination file left behind, got %v", entries)
	}
}

// --- download --------------------------------------------------------

func TestDownload_Progress(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	var lastDone, lastTotal int64
	progress := func(stage string, done, total int64) {
		if stage != "download" {
			t.Errorf("unexpected stage %q", stage)
		}
		lastDone, lastTotal = done, total
	}

	got, err := download(context.Background(), srv.Client(), srv.URL, progress)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("got %d bytes, want %d", len(got), len(body))
	}
	if lastDone != int64(len(body)) {
		t.Fatalf("progress done=%d, want %d", lastDone, len(body))
	}
	if lastTotal != int64(len(body)) {
		t.Fatalf("progress total=%d, want %d", lastTotal, len(body))
	}
}

func TestDownload_Offline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // now nothing is listening: connection refused

	_, err := download(context.Background(), http.DefaultClient, url, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	var offline *OfflineError
	if !errors.As(err, &offline) {
		t.Fatalf("expected *OfflineError, got %T: %v", err, err)
	}
}

// TestOfflineClassificationIsNarrow guards against the regression this
// package shipped with: isOffline used to match any net.Error, and because
// http.Client.Do wraps every transport-level failure in *url.Error (which
// itself implements net.Error via Timeout()/Temporary()), that matched
// almost everything — including a plain HTTP error status — and reported
// "no internet" on machines that were online the whole time. A server that
// responds at all, even with 403, proves the machine has connectivity, so
// it must never become *OfflineError. A dial to a closed port never
// establishes a connection at all, so it must.
func TestOfflineClassificationIsNarrow(t *testing.T) {
	t.Run("HTTP error status is not offline", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()

		_, err := download(context.Background(), srv.Client(), srv.URL, nil)
		if err == nil {
			t.Fatal("expected an error")
		}
		var offline *OfflineError
		if errors.As(err, &offline) {
			t.Fatalf("403 must not be classified as *OfflineError, got %v", err)
		}
		if !strings.Contains(err.Error(), "403") {
			t.Fatalf("error should name the status code, got %v", err)
		}
	})

	t.Run("dial to a closed port is offline", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := srv.URL
		srv.Close() // nothing listening: dial fails with connection refused

		_, err := download(context.Background(), http.DefaultClient, url, nil)
		if err == nil {
			t.Fatal("expected an error")
		}
		var offline *OfflineError
		if !errors.As(err, &offline) {
			t.Fatalf("a dial failure must be classified as *OfflineError, got %T: %v", err, err)
		}
	})
}

// TestDownloadFollowsRedirect proves a download follows a redirect (as
// GitHub release URLs always do, to release-assets.githubusercontent.com)
// all the way to a 200, rather than stopping at the 3xx.
func TestDownloadFollowsRedirect(t *testing.T) {
	body := []byte("final response body")
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer final.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer redirector.Close()

	got, err := download(context.Background(), redirector.Client(), redirector.URL, nil)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("got %q, want %q", got, body)
	}
}

// --- Install / Remove / Status, via fakes -------------------------------

func TestInstall_AlreadyInstalled_SkipsNetwork(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, TargetFile)
	if err := os.WriteFile(dest, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := newFakeRegistry()
	if err := reg.Set(FontDisplayName, dest); err != nil {
		t.Fatal(err)
	}

	opts := Options{
		FontsDir: dir,
		Registry: reg,
		GDI:      &fakeGDI{},
		// A URL that would fail loudly if Install ever dialed it.
		DownloadURL: "http://127.0.0.1:0/should-not-be-fetched",
	}
	res, err := Install(context.Background(), opts)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Installed {
		t.Fatal("expected Installed=false when already installed")
	}
	if res.FontPath != dest {
		t.Fatalf("FontPath=%q, want %q", res.FontPath, dest)
	}
}

func TestInstall_ChecksumMismatch(t *testing.T) {
	archive := buildZip(t, TargetFile, []byte("arbitrary content that will not match the pinned hash"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	opts := Options{
		FontsDir:    t.TempDir(),
		Registry:    newFakeRegistry(),
		GDI:         &fakeGDI{},
		DownloadURL: srv.URL,
	}
	_, err := Install(context.Background(), opts)
	if err == nil {
		t.Fatal("expected a checksum mismatch error")
	}
	var offline *OfflineError
	if errors.As(err, &offline) {
		t.Fatalf("did not expect an OfflineError: %v", err)
	}
}

func TestInstall_Offline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	opts := Options{
		FontsDir:    t.TempDir(),
		Registry:    newFakeRegistry(),
		GDI:         &fakeGDI{},
		DownloadURL: url,
	}
	_, err := Install(context.Background(), opts)
	var offline *OfflineError
	if !errors.As(err, &offline) {
		t.Fatalf("expected *OfflineError, got %T: %v", err, err)
	}
}

// TestInstall_FullRoundTrip exercises download -> verify -> extract ->
// write -> registry -> GDI end to end using a small fixture zip instead of
// the real multi-MB release asset. It sets Options.wantSHA256 to the
// fixture's own hash for the duration of the test; production code never
// sets that field (see its doc comment), and sha256Hex itself is never
// touched since it is now a const.
func TestInstall_FullRoundTrip(t *testing.T) {
	fontBytes := []byte("pretend this is a valid TTF")
	archive := buildZip(t, TargetFile, fontBytes)
	sum := sha256.Sum256(archive)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	dir := t.TempDir()
	reg := newFakeRegistry()
	gdi := &fakeGDI{}
	opts := Options{
		FontsDir:    dir,
		Registry:    reg,
		GDI:         gdi,
		DownloadURL: srv.URL,
		wantSHA256:  hex.EncodeToString(sum[:]),
	}

	res, err := Install(context.Background(), opts)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.Installed {
		t.Fatal("expected a fresh install")
	}
	dest := filepath.Join(dir, TargetFile)
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read installed font: %v", err)
	}
	if !bytes.Equal(got, fontBytes) {
		t.Fatalf("installed font content = %q, want %q", got, fontBytes)
	}
	if v, ok, _ := reg.Get(FontDisplayName); !ok || v != dest {
		t.Fatalf("registry value = %q, ok=%v, want %q, true", v, ok, dest)
	}
	if len(gdi.added) != 1 || gdi.added[0] != dest {
		t.Fatalf("GDI.AddFontResource calls = %v, want [%q]", gdi.added, dest)
	}
	if gdi.broadcasts != 1 {
		t.Fatalf("broadcasts = %d, want 1", gdi.broadcasts)
	}

	// Calling Install again must skip the network and GDI/registry work.
	res2, err := Install(context.Background(), opts)
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if res2.Installed {
		t.Fatal("second Install should report Installed=false")
	}
	if len(gdi.added) != 1 {
		t.Fatalf("second Install should not call AddFontResource again, got %v", gdi.added)
	}

	// Status agrees.
	st, err := Status(opts)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || st.FontPath != dest {
		t.Fatalf("Status = %+v, want Installed=true FontPath=%q", st, dest)
	}

	// Remove reverses it.
	if rmErr := Remove(opts); rmErr != nil {
		t.Fatalf("Remove: %v", rmErr)
	}
	if _, statErr := os.Stat(dest); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected font file removed, stat err = %v", statErr)
	}
	if _, ok, _ := reg.Get(FontDisplayName); ok {
		t.Fatal("expected registry value removed")
	}
	if len(gdi.removed) != 1 || gdi.removed[0] != dest {
		t.Fatalf("GDI.RemoveFontResource calls = %v, want [%q]", gdi.removed, dest)
	}

	st2, err := Status(opts)
	if err != nil {
		t.Fatalf("Status after remove: %v", err)
	}
	if st2.Installed {
		t.Fatal("expected Installed=false after Remove")
	}
}

func TestInstall_PolicyBlocked(t *testing.T) {
	fontBytes := []byte("pretend this is a valid TTF")
	archive := buildZip(t, TargetFile, fontBytes)
	sum := sha256.Sum256(archive)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	reg := newFakeRegistry()
	reg.setErr = &PolicyError{Err: errors.New("access denied"), ManualSteps: "do it by hand"}

	opts := Options{
		FontsDir:    t.TempDir(),
		Registry:    reg,
		GDI:         &fakeGDI{},
		DownloadURL: srv.URL,
		wantSHA256:  hex.EncodeToString(sum[:]),
	}
	_, err := Install(context.Background(), opts)
	var policyErr *PolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("expected *PolicyError, got %T: %v", err, err)
	}
	if policyErr.ManualSteps == "" {
		t.Fatal("expected non-empty ManualSteps")
	}
}

func TestRemove_NeverInstalled_IsIdempotent(t *testing.T) {
	opts := Options{
		FontsDir: t.TempDir(),
		Registry: newFakeRegistry(),
		GDI:      &fakeGDI{},
	}
	if err := Remove(opts); err != nil {
		t.Fatalf("Remove on a clean state should succeed, got %v", err)
	}
}
