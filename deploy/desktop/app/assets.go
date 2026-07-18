// Package app is the Wails packaging layer for the desktop personal gateway
// (design/desktop.md §10). It holds the embed.FS for the built SPA and the
// Wails app context (HTTP server lifecycle, native menu).
//
// NOT guarded by a build tag: Wails v2 supports macOS / Windows / Linux, so
// `go build ./...` on any platform resolves the wails dependency. The cgo /
// native WebView linkage only happens at final link time inside `wails build`,
// which selects the platform. This also lets the Wails CLI's binding
// generator (which runs without user-provided -tags) see the package.
//
// Build with:
//
//	make desktop-build             # macOS .app (calls wails build)
//	CGO_ENABLED=1 go build -o desktop-gateway ./cmd/desktop   # manual
package app

import "embed"

// dist/ is populated by scripts/build-desktop.sh (cp -R ../../desktop-ui/dist
// ../dist). The //go:embed all:dist directive picks up everything under it.
// When dist/ contains only .gitkeep (dev checkout, pre-build), Assets still
// embeds successfully with that single file — the Static() handler serves the
// "UI not built" fallback just like the filesystem path variant does.
//
//go:embed all:dist
var Assets embed.FS
