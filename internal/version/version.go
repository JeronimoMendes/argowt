// Package version exposes build metadata populated with -ldflags.
package version

import "fmt"

// Dev is the version of local builds without a release tag.
const Dev = "dev"

var (
	Version = Dev
	Commit  = "unknown"
	Date    = "unknown"
)

func String() string {
	return fmt.Sprintf("argowt %s (commit %s, built %s)", Version, Commit, Date)
}
