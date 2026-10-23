package issueflow

import (
	"testing"

	"github.com/piprim/git-zf/config"
)

func TestPullRequestHead(t *testing.T) {
	t.Parallel()

	project := func(id string) *config.IssueTrackerConfig {
		return &config.IssueTrackerConfig{URL: "https://github.com", Projects: []config.TrackerProject{{ID: id}}}
	}

	for name, tc := range map[string]struct {
		remote string
		cfg    *config.IssueTrackerConfig
		want   string
	}{
		"the remote is the project (scp-like URL)":    {"git@github.com:up/sandbox.git", project("up/sandbox"), "feat"},
		"the remote is the project (https URL)":       {"https://github.com/up/sandbox.git", project("up/sandbox"), "feat"},
		"the remote is a fork (scp-like URL)":         {"git@github.com:fork/sandbox.git", project("up/sandbox"), "fork:feat"},
		"the remote is a fork (https URL)":            {"https://github.com/fork/sandbox", project("up/sandbox"), "fork:feat"},
		"the remote is a fork (ssh URL, userinfo)":    {"ssh://git@github.com:22/fork/sandbox.git", project("up/sandbox"), "fork:feat"},
		"the owner matches whatever its case":         {"git@github.com:Up/sandbox.git", project("up/sandbox"), "feat"},
		"a renamed fork is still the owner's":         {"git@github.com:fork/other-name.git", project("up/sandbox"), "fork:feat"},
		"a percent-encoded path is still the owner's": {"https://github.com/fork/my%2Drepo.git", project("up/sandbox"), "fork:feat"},
		"a path remote is not a forge":                {"", project("up/sandbox"), "feat"},
		"a file URL is not a forge":                   {"file:///srv/git/up/sandbox.git", project("up/sandbox"), "feat"},
		"no project: the bare branch":                 {"git@github.com:fork/sandbox.git", &config.IssueTrackerConfig{}, "feat"},
		"two projects: the bare branch":               {"git@github.com:fork/sandbox.git", &config.IssueTrackerConfig{Projects: []config.TrackerProject{{ID: "a/b"}, {ID: "c/d"}}}, "feat"},
		"a project that is not owner/repo":            {"git@github.com:fork/sandbox.git", project("sandbox"), "feat"},
		"a forge under a path prefix": {"https://host/forge/fork/sandbox.git",
			&config.IssueTrackerConfig{URL: "https://host/forge/", Projects: []config.TrackerProject{{ID: "up/sandbox"}}}, "fork:feat"},
		"a forge behind basic auth in the tracker URL": {"https://host/fork/sandbox.git",
			&config.IssueTrackerConfig{URL: "https://user:pw@host", Projects: []config.TrackerProject{{ID: "up/sandbox"}}}, "fork:feat"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, dir := newPushedRepo(t) // origin is a path
			if tc.remote != "" {
				gitAt(t, dir, "remote", "set-url", "origin", tc.remote)
			}
			if got := PullRequestHead(c, tc.cfg, "feat"); got != tc.want {
				t.Errorf("head = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("no remote: the bare branch", func(t *testing.T) {
		t.Parallel()
		c, dir := newPushedRepo(t)
		gitAt(t, dir, "remote", "remove", "origin")
		if got := PullRequestHead(c, project("up/sandbox"), "feat"); got != "feat" {
			t.Errorf("head = %q", got)
		}
	})
}

func TestForkUpstream(t *testing.T) {
	t.Parallel()

	project := func(url, id string) *config.IssueTrackerConfig {
		return &config.IssueTrackerConfig{URL: url, Projects: []config.TrackerProject{{ID: id}}}
	}

	for name, tc := range map[string]struct {
		remote string
		cfg    *config.IssueTrackerConfig
		want   string
	}{
		"a fork (scp-like URL)":           {"git@github.com:fork/sandbox.git", project("https://github.com", "up/sandbox"), "git@github.com:up/sandbox.git"},
		"a renamed fork keeps the suffix": {"https://github.com/fork/other.git", project("https://github.com", "up/sandbox"), "https://github.com/up/sandbox.git"},
		"a fork without .git":             {"https://github.com/fork/sandbox", project("https://github.com", "up/sandbox"), "https://github.com/up/sandbox"},
		"a forge under a path prefix":     {"https://host/forge/fork/sandbox.git", project("https://host/forge", "up/sandbox"), "https://host/forge/up/sandbox.git"},
		"the remote is the project: none": {"git@github.com:up/sandbox.git", project("https://github.com", "up/sandbox"), ""},
		"a path remote: none":             {"", project("https://github.com", "up/sandbox"), ""},
		"a percent-encoded path: none":    {"https://github.com/fork/my%2Drepo.git", project("https://github.com", "up/sandbox"), ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, dir := newPushedRepo(t)
			if tc.remote != "" {
				gitAt(t, dir, "remote", "set-url", "origin", tc.remote)
			}
			if got := ForkUpstream(c, tc.cfg); got != tc.want {
				t.Errorf("upstream = %q, want %q", got, tc.want)
			}
		})
	}
}
