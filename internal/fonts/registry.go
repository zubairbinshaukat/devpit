package fonts

// Registry abstracts the per-user font registry key
// (HKCU\Software\Microsoft\Windows NT\CurrentVersion\Fonts) so Install,
// Remove and Status can be tested with a fake instead of touching the real
// Windows registry.
type Registry interface {
	// Set writes name = value.
	Set(name, value string) error
	// Delete removes name. It is not an error if name does not exist.
	Delete(name string) error
	// Get reads name. ok is false if it does not exist.
	Get(name string) (value string, ok bool, err error)
}

// GDI abstracts the Win32 calls that make a freshly installed font usable
// without a reboot, so tests can substitute a no-op fake instead of
// mutating the real OS font state.
type GDI interface {
	// AddFontResource calls AddFontResourceW for path.
	AddFontResource(path string) error
	// RemoveFontResource calls RemoveFontResourceW for path.
	RemoveFontResource(path string) error
	// BroadcastFontChange notifies running programs (Windows Terminal
	// included) that the font table changed, via
	// SendMessageTimeoutW(HWND_BROADCAST, WM_FONTCHANGE). It is
	// best-effort and does not report failures: a program that missed the
	// broadcast simply keeps its old font list until restarted.
	BroadcastFontChange()
}
