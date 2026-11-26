// Package version holds the build version.
package version

// Number is overridden at build time with -ldflags "-X .../version.Number=x.y.z".
var Number = "0.1.0"
