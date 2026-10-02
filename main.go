// Command devpit is a toolkit for developers on Windows: the right account in
// every folder, disk space back, stuck ports freed, tools updated and big
// folders moved between PCs, from one menu.
package main

import (
	"os"

	devpit "github.com/zubairbinshaukat/devpit/cmd/devpit"
)

func main() {
	os.Exit(devpit.Execute())
}
