// Command desktop is the CLI / dev entrypoint for the desktop personal gateway.
// The shared application logic lives in internal/desktopapp and is also used
// by the Wails .app entrypoint at deploy/desktop/main.go.
package main

import "voxeltoad/internal/desktopapp"

func main() {
	desktopapp.Main()
}
