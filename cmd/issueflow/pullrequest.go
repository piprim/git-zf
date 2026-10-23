package issueflow

import (
	"net/url"
	"strings"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
)

// PullRequestHead is the head of branchName's pull request on the forge
// hosting the tracker's project: the bare branch when this clone's remote
// is that project, "owner:branch" when it is a fork of it (its owner
// differs; a fork cannot share its owner). A remote that is not a forge
// URL (a path), no remote or no single project keep the bare branch.
func PullRequestHead(c *git.Client, cfg *config.IssueTrackerConfig, branchName string) string {
	if owner, _ := fork(c, cfg); owner != "" {
		return owner + ":" + branchName
	}

	return branchName
}

// ForkUpstream is the URL of the tracker's project on the forge when this
// clone's remote is a fork of it: the remote's URL with the project in
// place of its owner/repo. "" when the remote is the project, a path, or
// absent: the remote is then the place to fetch from.
func ForkUpstream(c *git.Client, cfg *config.IssueTrackerConfig) string {
	_, upstream := fork(c, cfg)

	return upstream
}

// fork reports the owner of this clone's remote and the URL of the
// tracker's project on the same forge when the remote is a fork of that
// project; both "" otherwise.
func fork(c *git.Client, cfg *config.IssueTrackerConfig) (owner, upstream string) {
	if len(cfg.Projects) != 1 {
		return "", ""
	}
	project := cfg.Projects[0].ID
	projectOwner, _, ok := strings.Cut(project, "/")
	remote, err := c.RemoteURL()
	if !ok || err != nil {
		return "", ""
	}

	owner, repo := remoteOwnerRepo(remote, cfg.URL)
	if owner == "" || strings.EqualFold(owner, projectOwner) {
		return "", ""
	}
	if strings.HasSuffix(repo, ".git") {
		project += ".git"
	}
	i := strings.LastIndex(remote, owner+"/"+repo)
	if i < 0 { // the URL encodes its path: the fork is known, its upstream is not derived
		return owner, ""
	}

	return owner, remote[:i] + project + remote[i+len(owner)+1+len(repo):]
}

// remoteOwnerRepo splits a forge remote into its owner and repository
// segments: "https://host/owner/repo.git", "ssh://git@host/owner/repo.git"
// or "git@host:owner/repo.git". A forge served under a path (trackerURL
// "https://host/forge") has it stripped. A path, or anything else, is "".
func remoteOwnerRepo(remote, trackerURL string) (owner, repo string) {
	var p string
	switch {
	case strings.Contains(remote, "://"):
		u, err := url.Parse(remote)
		if err != nil || u.Host == "" {
			return "", ""
		}
		p = u.Path
		if t, err := url.Parse(trackerURL); err == nil {
			p = strings.TrimPrefix(p, strings.TrimSuffix(t.Path, "/"))
		}
	default: // scp-like [user@]host:owner/repo.git; a path has no colon before its first slash
		host, path, ok := strings.Cut(remote, ":")
		if !ok || strings.Contains(host, "/") {
			return "", ""
		}
		p = path
	}

	segs := strings.Split(strings.Trim(p, "/"), "/")
	if len(segs) != 2 {
		return "", ""
	}

	return segs[0], segs[1]
}
