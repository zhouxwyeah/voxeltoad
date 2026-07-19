// Package buildinfo holds build-time stamps for the binaries. The desktop
// gateway serves its Version from /api/v1/health and logs it at startup so a
// stale binary (e.g. one predating newly added endpoints, which surfaces as
// API 404s in the UI) is identifiable from the app itself.
package buildinfo

// Version is the build stamp. It defaults to "dev" for plain `go build` /
// `go run`; packaging scripts inject the git commit via
// -ldflags "-X voxeltoad/internal/buildinfo.Version=<commit>".
var Version = "dev"
