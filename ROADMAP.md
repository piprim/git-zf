# Roadmap

1. Drop the reviews table now. It is self-contained, the chain already has the data, and it removes the cache-drift class of bug we just fixed in review status.
2. Then branch refs as chains, absorbing branches, issues and issue_relations, with command_history moved to a file in the same change.

## Enhancement

-  No command edits the title or description of a repo-only issue. An issue edit form would be one small extra task if you want it. + A close-by-ID command and a branch naming fallback (non-Latin title as a backlog item).
- In-repo issue must carry on author and assignee: Who opened it and who works on it, from git identity. Lets `issue list` filter 'mine' without a tracker.
- `git zf init` installs hooks under the per-worktree git dir when run inside a linked worktree; git reads hooks from the common dir. Run `init` from the main checkout for now.
- AI assistant. See https://github.com/rshdhere/vibecheck
- Implement bug tracking into git repo and by-directionnal syncing with remote trackers like https://github.com/git-bug/git-bug does but without the same feature, git-zf keeps his specific workflow.
- git zf branch close <branch-name>
  No menu entry or picker. The command takes exactly one name and is not in the git zf branch menu. A picker over in-progress branches is a small addition

### Deferred minors
- git zf -d issue new skips the form, because the debug flag counts as a flag.
- issue list --status with --stdout or --json filters by branch status, while the interactive table follows the issue state.
- Offline output is noisy: git's error block can print three times for one command.
- Reconcile does not check that both chains share a root, so a force-pushed foreign chain would be merged in. Worth fixing before Plan 2.
- Junk refs on the remote under refs/zf/issues/ are imported and warn on every command.
- Titles and comments from the remote reach the terminal unsanitized, as tracker titles already do.
- Two identical issue new calls within one second collide, and the second fails with a raw git error.
- docs/issue-refs.md says every issue command fetches the refs; issue new does not.
- The list joins on the 7-character ID, and JSON shows "labels": null for rows without a repository issue.
- issue list shows no creation date for an issue that has no branch yet.


## To be discuss

Add the trackers for “Under development”, “To be reviewed/tested”, “Under review”, “Test/review” to the redimne conf to automate the code review workflow.
Is it possible to do the same with Forgejo with labels or projects ?
