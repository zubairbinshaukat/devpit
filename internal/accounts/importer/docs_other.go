//go:build !windows

package importer

// documentsFolder has no known-folder lookup off Windows.
func documentsFolder() string { return "" }
