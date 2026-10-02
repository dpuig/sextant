// Package version holds the build version, overridden via -ldflags.
package version

// Version is set at build time: -ldflags "-X github.com/dpuig/sextant/pkg/version.Version=v1.2.3".
var Version = "dev"
