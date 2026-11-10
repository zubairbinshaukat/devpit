//go:build !windows

package host

// linkRows knows nothing outside Windows: every link is unknown.
func linkRows([]uint32) map[uint32]linkRow { return nil }
