---
title: "git zf review"
sidebar_label: "review"
---

## git zf review

Manage the code review lifecycle for an issue branch

### Synopsis

Peer-to-peer code review with no server-side component. The state of a
review lives in the repository, under refs/zf/reviews/&lt;IssueID&gt;, one commit
per action, pushed to and fetched from the remote. Nothing about a review is
kept anywhere else, and actions taken on two machines at once are merged,
never overwritten.

A review round:

  1. Developer:  git zf review request   locks the branch and asks for a
                                         review
  2. Reviewer:   git fetch && git zf review start
                                         creates &lt;IssueID&gt;@review from the
                                         locked snapshot
  3. Reviewer:   git zf review approve   the branch is ready to close
             or  git zf review reject    changes requested, the branch is
                                         unlocked
  4. Developer:  pushes fixes and requests again: the round counter goes up

While a branch is locked, the pre-push hook installed by git zf init rejects
pushes to it. Reviewer commits left on &lt;IssueID&gt;@review are merged into the
branch by git zf review sync, or by git zf issue close at the end.

Without a subcommand a menu offers these actions.

```
git zf review [flags]
```

### Options

```
  -h, --help   help for review
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf](git_zf.md)	 - Git Zen workFlow: issue → branch → commit → review → close
* [git zf review approve](git_zf_review_approve.md)	 - Approve a review — signals the branch is ready to close
* [git zf review fetch](git_zf_review_fetch.md)	 - Sync review refs with the remote: fetch, merge, and push the ones it lacks
* [git zf review list](git_zf_review_list.md)	 - List all issues currently in review or approved
* [git zf review reject](git_zf_review_reject.md)	 - Request changes on a review — unlocks the branch for the next iteration
* [git zf review request](git_zf_review_request.md)	 - Submit an issue branch for code review (locks the branch)
* [git zf review start](git_zf_review_start.md)	 - Begin reviewing an issue (creates &lt;IssueID&gt;@review branch from the locked snapshot)
* [git zf review status](git_zf_review_status.md)	 - Show the full review history for an issue
* [git zf review sync](git_zf_review_sync.md)	 - Bring a branch up to date: merge pending reviewer commits, then parent drift
* [git zf review track](git_zf_review_track.md)	 - Track the current branch with git zf (for branches created with plain git checkout)

