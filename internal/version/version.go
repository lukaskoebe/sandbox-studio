// Package version holds build metadata set via -ldflags.
package version

// Version is overridden at build time with -ldflags "-X .../internal/version.Version=v1.2.3".
var Version = "dev"

// BaseImage is the digest-pinned base image reference a release build uses by default, set
// with -ldflags "-X .../internal/version.BaseImage=ghcr.io/lukaskoebe/sandbox-studio-base@sha256:…".
// Empty in development builds.
var BaseImage = ""

// BrowserImage is the digest-pinned browser image that goes with BaseImage, set the same
// way. Empty in development builds.
var BrowserImage = ""
