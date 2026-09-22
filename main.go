// Command devpit is a Windows-first terminal app that frees disk space,
// fixes stuck ports and keeps developer tools up to date.
package main

import (
	"os"

	devpit "github.com/zubairbinshaukat/devpit/cmd/devpit"
)

func main() {
	os.Exit(devpit.Execute())
}
