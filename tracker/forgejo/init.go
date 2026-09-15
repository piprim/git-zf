package forgejo

import "github.com/piprim/git-zf/tracker"

// giteaType is registered as an alias: Gitea exposes the same API v1 surface
// this adapter relies on, so one implementation serves both.
const giteaType = "gitea"

//nolint:gochecknoinits // Register pattern needs it
func init() {
	tracker.Register(trackerType, New)
	tracker.Register(giteaType, New)
}
