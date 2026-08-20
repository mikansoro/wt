// Package version holds the build-time version metadata printed by `wt version`.
package version

import "runtime/debug"

// Version, Commit, and Date are stamped at build time via:
//
//	-ldflags "-X wt/internal/version.Version=... -X wt/internal/version.Commit=... -X wt/internal/version.Date=..."
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// String returns the version to display. Binaries built with `go install` carry no
// ldflags, so when Version is still the zero-value "dev", this falls back to the module
// version debug.ReadBuildInfo embeds automatically.
func String() string {
	if Version != "dev" {
		return Version
	}

	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}

	return Version
}
