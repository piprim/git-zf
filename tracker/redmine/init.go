package redmine

import "github.com/piprim/git-zf/tracker"

//nolint:gochecknoinits // Register pattern need it
func init() {
	tracker.Register(trackerType, New)
}
