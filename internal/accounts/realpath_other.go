//go:build !windows

package accounts

// RealPath returns folder normalized. Junctions, subst and mapped drives are
// Windows things; elsewhere the path is taken as given.
func RealPath(folder string) (string, error) {
	return NormalizeFolder(folder, "")
}
