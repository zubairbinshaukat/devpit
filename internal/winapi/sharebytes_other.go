//go:build !windows

package winapi

// ShareBytes is a per-share SMB byte counter. Outside Windows it cannot be
// opened.
type ShareBytes struct{}

// openShareBytes is not implemented outside Windows.
func openShareBytes(string, func(string) bool) (*ShareBytes, error) { return nil, ErrUnsupported }

// Read is not implemented outside Windows.
func (*ShareBytes) Read() (uint64, error) { return 0, ErrUnsupported }

// Close does nothing outside Windows.
func (*ShareBytes) Close() {}
