package github

import "github.com/piprim/git-zf/tracker"

//nolint:gochecknoinits // Register pattern needs it
func init() {
	tracker.Register(trackerType, New)
}
