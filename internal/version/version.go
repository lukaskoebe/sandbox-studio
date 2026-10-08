// Package version holds build metadata set via -ldflags.
package version

// Version is overridden at build time with -ldflags "-X .../internal/version.Version=v1.2.3".
var Version = "dev"
