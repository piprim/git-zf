# Roadmap

## Enhancement

- Issue mirror, repo <-> tracker (first version shipped). Spec: docs/superpowers/specs/2026-10-06-issue-tracker-mirror-design.md. Left out of its first version:
  - Several projects: project-aware tracker calls, project-qualified slugs, a project picker in `issue new`.
  - Sync of title, description, labels and comments.
  - `issue start` picker reading the mirror instead of the live "assigned to me" listing.
  - A warning from the reconcile when two records link the same tracker number (one clone exported while another imported the new issue before fetching).
- In-repo issue must carry on author and assignee: Who opened it and who works on it, from git identity. Lets `issue list` filter 'mine'. Comes after the issue mirror.
- AI assistant. See https://github.com/rshdhere/vibecheck
- git zf branch close <branch-name>
  No menu entry or picker. The command takes exactly one name and is not in the git zf branch menu. A picker over in-progress branches is a small addition

### Deferred minors
- Two identical issue new calls within one second collide, and the second fails with a raw git error.
- docs/issue-refs.md says every issue command fetches the refs; issue new does not.
- The list joins on the 7-character ID, and JSON shows "labels": null for rows without a repository issue.
- issue list shows no creation date for an issue that has no branch yet.


## To be discuss

Add the trackers for “Under development”, “To be reviewed/tested”, “Under review”, “Test/review” to the redimne conf to automate the code review workflow.
Is it possible to do the same with Forgejo with labels or projects ?

