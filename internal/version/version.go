// Package version holds build-time version information injected via
// -ldflags "-X" at build time.
package version

var (
	// Version is the semantic version, set via -ldflags "-X" at build time.
	Version = "0.0.0-dev"
	// Commit is the short git commit hash, set via -ldflags "-X" at build time.
	Commit = ""
)

// String returns Version, followed by Commit in parentheses when set.
func String() string {
	if Commit == "" {
		return Version
	}
	return Version + " (" + Commit + ")"
}
