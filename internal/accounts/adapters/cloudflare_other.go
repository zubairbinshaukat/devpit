//go:build !windows

package adapters

import "github.com/zubairbinshaukat/devpit/internal/accounts"

// wranglerTrueCase only normalizes the folder off Windows: letter case on
// disk is a Windows question.
func wranglerTrueCase(folder string) string {
	if n, err := accounts.NormalizeFolder(folder, ""); err == nil {
		return n
	}
	return folder
}
