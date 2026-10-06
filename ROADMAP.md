# Roadmap

## Enhancement

- Issue mirror, repo <-> tracker. Spec: docs/superpowers/specs/2026-10-06-issue-tracker-mirror-design.md. Left out of its first version:
  - Several projects: project-aware tracker calls, project-qualified slugs, a project picker in `issue new`.
  - Sync of title, description, labels and comments.
  - `issue start` picker reading the mirror instead of the live "assigned to me" listing.
- In-repo issue must carry on author and assignee: Who opened it and who works on it, from git identity. Lets `issue list` filter 'mine'. Comes after the issue mirror.
- AI assistant. See https://github.com/rshdhere/vibecheck
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
