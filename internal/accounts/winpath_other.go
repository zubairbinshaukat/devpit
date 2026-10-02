//go:build !windows

package accounts

// posixRoots: off Windows a path starting with a single `/` is absolute (on
// the volume `/`), so the Linux CI runner's temp folders work as folders.
const posixRoots = true
