// Command desktop is the Wails .app entrypoint for the desktop personal gateway.
// The shared application logic lives in internal/desktopapp; this file is a
// thin package main wrapper so that wails build (run from deploy/desktop) sees
// a main package in the project directory and can generate bindings correctly.
//
// Build with: make desktop-build
package main

import (
	"voxeltoad/deploy/desktop/app"
	"voxeltoad/internal/desktopapp"
)

// Ensure app is referenced so the embed.FS and Wails App type are linked into
// the main binary. Without this, Go may drop the package from the build.
var _ = app.Assets

func main() {
	desktopapp.Main()
}
