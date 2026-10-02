//go:build !windows

package accounts

// LongPath returns folder normalized: 8.3 short names are a Windows thing.
// A path that does not parse comes back as given.
func LongPath(folder string) string {
	norm, err := NormalizeFolder(folder, "")
	if err != nil {
		return folder
	}
	return norm
}
