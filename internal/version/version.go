// Package version holds build metadata for the TenSunS Go process.
package version

// These values are overridden at build time with -ldflags.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)
