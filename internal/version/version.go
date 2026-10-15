// Package version exposes build metadata populated with -ldflags.
package version

import "fmt"

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func String() string {
	return fmt.Sprintf("argowt %s (commit %s, built %s)", Version, Commit, Date)
}
