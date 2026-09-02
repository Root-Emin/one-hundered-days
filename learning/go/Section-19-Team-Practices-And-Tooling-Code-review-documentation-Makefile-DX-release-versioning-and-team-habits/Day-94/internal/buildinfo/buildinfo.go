// Package buildinfo holds the values stamped into the binary at link time.
//
// They are variables, not constants, because -ldflags -X can only overwrite a
// variable. The defaults are what a `go run` build reports, and they say so
// rather than pretending to be a release.
package buildinfo

import "fmt"

// Version is the semantic version of this build.
var Version = "dev"

// Commit is the git commit this was built from.
var Commit = "none"

// BuildTime is the COMMIT time, in RFC 3339.
//
// The commit time and not the build time: a build timestamp changes on every
// build, which makes the binary unreproducible by construction.
var BuildTime = "unknown"

// String renders the build for a -version flag or a startup log line.
func String() string {
	return fmt.Sprintf("%s (%s, built from commit time %s)", Version, Commit, BuildTime)
}
