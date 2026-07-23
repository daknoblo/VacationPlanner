// Package version exposes the application's build version.
package version

import "runtime/debug"

// Version is the application version. It defaults to "dev" and can be overridden
// at build time via
//
//	-ldflags "-X github.com/daknoblo/vacationplanner/internal/version.Version=v1.2.3"
var Version = "dev"

// String returns the version. When no explicit version was set at build time it
// falls back to the VCS revision embedded by the Go toolchain (go build), so a
// locally built binary still reports something meaningful.
func String() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		var rev, modified string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
		if rev != "" {
			if len(rev) > 12 {
				rev = rev[:12]
			}
			if modified == "true" {
				return "dev+" + rev + "-dirty"
			}
			return "dev+" + rev
		}
	}
	return Version
}
