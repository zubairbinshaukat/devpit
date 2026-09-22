// Package fonts installs and removes the icon font Devpit uses for its
// nicer, Nerd-Font UI tier (plan.md section 5). It downloads a pinned
// release asset from GitHub, verifies its checksum, extracts a single TTF,
// and registers it per-user via the HKCU font registry key plus
// AddFontResourceW/WM_FONTCHANGE so running programs notice it without a
// reboot.
//
// The package never writes to Devpit's config; callers read the returned
// Result/Status and decide what to persist.
package fonts

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Release metadata for the font asset. The tag and checksum are pinned in
// source deliberately: Install refuses to trust a mismatched download.
const (
	// ReleaseTag is the nerd-fonts GitHub release Devpit installs from.
	ReleaseTag = "v3.5.1"
	// AssetName is the release asset containing every Nerd Font symbol
	// glyph as standalone patched fonts.
	AssetName = "NerdFontsSymbolsOnly.zip"
	// TargetFile is the single TTF Devpit extracts from AssetName and
	// installs. Everything else in the zip is discarded.
	TargetFile = "SymbolsNerdFontMono-Regular.ttf"
	// FontDisplayName is the registry value name (and Windows font family
	// display name) Devpit registers the font under.
	FontDisplayName = "Symbols Nerd Font Mono (TrueType)"

	downloadURL = "https://github.com/ryanoasis/nerd-fonts/releases/download/" + ReleaseTag + "/" + AssetName

	// downloadTimeout bounds a single download attempt. It is generous on
	// purpose (a ~3 MB file on a slow link should never trip it) but it
	// guarantees Install eventually gives up instead of hanging forever if
	// a connection stalls without erroring. It is applied as a context
	// deadline, not as http.Client.Timeout, so it never races the
	// server-initiated TLS renegotiation handshake some CDNs perform
	// mid-connection (see defaultHTTPClient).
	downloadTimeout = 3 * time.Minute

	// userAgent is sent on every request this package makes. GitHub's edge
	// (and some CDNs behind it) can be stricter about bare/empty
	// User-Agent headers than about identifying ones, so Install always
	// sends one instead of relying on Go's default (empty).
	userAgent = "devpit-font-installer (+https://github.com/zubairbinshaukat/devpit)"
)

// sha256Hex is the pinned SHA-256 of AssetName as published for ReleaseTag.
// Computed 2026-09-22 via:
//
//	curl -sL -o NerdFontsSymbolsOnly.zip <downloadURL>
//	sha256sum NerdFontsSymbolsOnly.zip
//
// It is a const: production Install always verifies against exactly this
// value. Tests that need a full Install() round-trip against a small fixture
// zip (rather than bundling the real multi-MB release asset into testdata)
// set Options.wantSHA256 instead of touching package state.
const sha256Hex = "fdca3682534f6f65e1ccb2345b0362ccf67d9b8eca7c8025330946e93e2473bc"

// State reports whether the font is currently installed for the current
// user. It is returned by Status; the type is not named Status itself
// because Go does not allow a type and a function to share a package-level
// identifier.
type State struct {
	Installed bool
	// FontPath is the full path to the installed TTF. Empty when not
	// installed.
	FontPath string
}

// Result is returned by Install.
type Result struct {
	// Installed is true when this call performed a fresh install. It is
	// false when the font was already installed and Install skipped the
	// download.
	Installed bool
	FontPath  string
	// RegistryValue is the registry value name Install wrote (or found
	// already present), i.e. FontDisplayName.
	RegistryValue string
}

// Progress reports download progress. stage is a short machine-readable
// name ("download" today); total is 0 when the server did not send a
// Content-Length.
type Progress func(stage string, done, total int64)

// Options configures Install, Remove and Status. The zero value is valid:
// every field is defaulted from the real environment (Windows registry,
// real GDI calls, %LOCALAPPDATA% fonts directory, http.DefaultClient).
//
// HTTPClient, FontsDir, Registry and GDI exist so tests can substitute
// fakes instead of touching the real network, registry or running Windows
// session. DownloadURL additionally lets tests point Install at an
// httptest.Server instead of GitHub; it defaults to the real release
// asset URL.
type Options struct {
	HTTPClient  *http.Client
	FontsDir    string
	Registry    Registry
	GDI         GDI
	Progress    Progress
	DownloadURL string

	// wantSHA256 overrides sha256Hex when non-empty. It exists only for
	// tests that exercise Install end to end against a small fixture zip
	// instead of the real release asset; production code never sets it, so
	// Install always verifies against the pinned const.
	wantSHA256 string
}

// checksum returns the SHA-256 Install must verify a download against: the
// test override when set, otherwise the pinned sha256Hex.
func (o Options) checksum() string {
	if o.wantSHA256 != "" {
		return o.wantSHA256
	}
	return sha256Hex
}

func (o Options) withDefaults() Options {
	if o.HTTPClient == nil {
		o.HTTPClient = defaultHTTPClient()
	}
	if o.FontsDir == "" {
		o.FontsDir = defaultFontsDir()
	}
	if o.Registry == nil {
		o.Registry = newRegistry()
	}
	if o.GDI == nil {
		o.GDI = newGDI()
	}
	if o.DownloadURL == "" {
		o.DownloadURL = downloadURL
	}
	return o
}

// defaultFontsDir returns %LOCALAPPDATA%\Microsoft\Windows\Fonts, the
// per-user font install directory that does not require admin rights.
func defaultFontsDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Windows", "Fonts")
}

// defaultHTTPClient builds the client Install uses when the caller does not
// supply one. It starts from http.DefaultTransport (so proxy handling via
// http.ProxyFromEnvironment, dial/keepalive timeouts and HTTP/2 all behave
// exactly as any other well-behaved Go HTTP client) and changes exactly one
// thing: it allows TLS renegotiation.
//
// That change is load-bearing, not defensive: GitHub release downloads
// redirect to Azure Blob Storage (release-assets.githubusercontent.com),
// which requests a mid-connection TLS renegotiation. crypto/tls's default
// (tls.RenegotiateNever) rejects that request, and the server then resets
// the connection — which net/http surfaces as a plain read error, and which
// this package's own isOffline used to misclassify as "no internet" (see
// download.go). A machine with a perfectly good connection would report
// "Offline" on every single install attempt. RenegotiateFreelyAsClient
// matches what curl/schannel and browsers already do against this exact
// endpoint, so it costs nothing in the common (non-renegotiating) case and
// fixes the common (renegotiating) one.
//
// No client-level Timeout is set here: a fixed client.Timeout races the
// renegotiation handshake and any other legitimate slow-but-progressing
// transfer. download() instead bounds the whole attempt with a generous
// context deadline (downloadTimeout), which does not have that problem.
func defaultHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		MinVersion:    tls.VersionTLS12,
		Renegotiation: tls.RenegotiateFreelyAsClient,
	}
	return &http.Client{Transport: transport}
}

func destPath(opts Options) string {
	return filepath.Join(opts.FontsDir, TargetFile)
}

// Status reports whether Symbols Nerd Font Mono is installed: a registry
// value must exist under
// HKCU\Software\Microsoft\Windows NT\CurrentVersion\Fonts and the TTF must
// exist on disk. Either alone is treated as not installed, so a half-done
// prior install is retried rather than silently skipped.
func Status(opts Options) (State, error) {
	opts = opts.withDefaults()
	return statusFor(opts)
}

func statusFor(opts Options) (State, error) {
	_, ok, err := opts.Registry.Get(FontDisplayName)
	if err != nil {
		return State{}, fmt.Errorf("fonts: read registry: %w", err)
	}
	if !ok {
		return State{}, nil
	}
	dest := destPath(opts)
	if _, err := os.Stat(dest); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("fonts: stat %s: %w", dest, err)
	}
	return State{Installed: true, FontPath: dest}, nil
}

// Install downloads, verifies and installs Symbols Nerd Font Mono for the
// current user, skipping the network entirely if it is already installed.
//
// Failure modes callers should handle specially:
//   - *OfflineError: the network is unreachable. Skip silently, let the
//     user retry from Settings.
//   - *PolicyError: the registry write was blocked (Group Policy). Show
//     PolicyError.ManualSteps.
//   - any other error wraps a checksum mismatch, a corrupt archive, or a
//     filesystem error, and should be reported as a failure.
func Install(ctx context.Context, opts Options) (Result, error) {
	opts = opts.withDefaults()

	st, err := statusFor(opts)
	if err != nil {
		return Result{}, err
	}
	if st.Installed {
		return Result{Installed: false, FontPath: st.FontPath, RegistryValue: FontDisplayName}, nil
	}

	wantHash := opts.checksum()
	if wantHash == "" {
		// Fail closed: never install a font whose checksum was not
		// verified against a value pinned in source.
		return Result{}, errors.New("fonts: checksum not pinned")
	}

	data, err := download(ctx, opts.HTTPClient, opts.DownloadURL, opts.Progress)
	if err != nil {
		return Result{}, err
	}
	if verErr := verifySHA256(data, wantHash); verErr != nil {
		return Result{}, verErr
	}
	ttf, err := extractFont(data, TargetFile)
	if err != nil {
		return Result{}, err
	}

	if err := os.MkdirAll(opts.FontsDir, 0o750); err != nil {
		return Result{}, fmt.Errorf("fonts: create %s: %w", opts.FontsDir, err)
	}
	dest := destPath(opts)
	if err := writeFileAtomic(dest, ttf, 0o644); err != nil {
		return Result{}, fmt.Errorf("fonts: write %s: %w", dest, err)
	}

	if err := opts.Registry.Set(FontDisplayName, dest); err != nil {
		return Result{}, err
	}
	if err := opts.GDI.AddFontResource(dest); err != nil {
		return Result{}, fmt.Errorf("fonts: register with session: %w", err)
	}
	opts.GDI.BroadcastFontChange()

	return Result{Installed: true, FontPath: dest, RegistryValue: FontDisplayName}, nil
}

// Remove reverses Install: it unregisters the font from the running
// session, deletes the registry value and deletes the TTF. Each step is
// best-effort and idempotent so Remove can be called even when Install
// only partially completed.
func Remove(opts Options) error {
	opts = opts.withDefaults()
	dest := destPath(opts)

	var errs []error
	if err := opts.GDI.RemoveFontResource(dest); err != nil {
		errs = append(errs, fmt.Errorf("fonts: unregister from session: %w", err))
	}
	if err := opts.Registry.Delete(FontDisplayName); err != nil {
		errs = append(errs, fmt.Errorf("fonts: delete registry value: %w", err))
	}
	if err := os.Remove(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("fonts: delete %s: %w", dest, err))
	}
	opts.GDI.BroadcastFontChange()

	return errors.Join(errs...)
}
