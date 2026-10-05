# Branch refs

git-zf records the branches of an issue in the repository as a chain of commits
under `refs/zf/branches/<slug>`, where `<slug>` is the issue's ID as it appears
in the branch name. The chain holds every branch started for the issue, its
status, and what git-zf knows about the issue itself: title, parent, tracker
type. This page describes the format, for people reading it with plain git:

```
git log refs/zf/branches/<slug>
git show <commit>:op.json
```

Nothing about a branch is kept outside these refs. There is no local database.

## Layout

| Ref | Content |
|---|---|
| `refs/zf/branches/<slug>` | The local chain of issue `<slug>`. |
| `refs/remotes/<remote>/zf/branches/<slug>` | What the remote had at the last fetch or push. Never edited by hand. |

A branch ref is never deleted. A merged or abandoned branch stays in its
chain, with its status.

The slug is the first segment of the branch name: `42@feat@add-login` and its
variant `42@feat@add-login@spike` both belong to the chain `42`.

## Ops

Every action is one commit. Its tree holds one file, `op.json`; its parent is
the previous action. Author and committer are the user's git identity.

```json
{"v":1,"type":"start","at":"2026-10-05T10:00:00Z","author":"Pi <pi@example.org>","branch":"42.1@feat@login-form","branch_type":"feat","title":"Login form","parent":"42"}
```

| `type` | Fields | Written by | Effect |
|---|---|---|---|
| `start` | `branch`, `branch_type`, `title`, `parent`, `tracker_type`, `issue_id` | `git zf issue start`, `git zf branch new`, `git zf issue track` | Tracks `branch` as in progress. First commit of the chain for the issue's first branch. |
| `set_status` | `branch`, `status` | `git zf issue close`, `git zf branch close`, `git zf branch prune`, `git zf branch prune-tracker` | Sets the status of `branch`: `in_progress`, `merged` or `closed`. |
| `merge` | none | sync | Two-parent commit joining actions made on two clones. |

`v` is the format version, currently 1. `at` is RFC 3339, UTC.

`parent` is the slug of the parent issue, for a sub-task. `tracker_type` names
the tracker the issue came from (`redmine`, `github`, `forgejo`), and is absent
for an issue typed by hand. `issue_id` is the full ID of the issue stored in the
repository (`refs/zf/issues/<id>`), when there is one.

## Reading a chain

The current state is the fold of all ops: a commit is applied after its
parents; commits with no order between them (made on two clones before either
synced) are applied by `at`, then by commit ID. Every clone computes the same
state:

- a second `start` for a branch the chain already knows adds nothing and
  reopens nothing. Two clones that track the same branch end with one entry;
- `title`, `parent`, `tracker_type` and `issue_id` keep the first value any
  `start` gave them;
- of two `set_status` made at once, the later one wins;
- a `set_status` for a branch the chain does not know is ignored;
- reopening a merged or closed branch is a `set_status in_progress`, which
  `git zf issue start` writes when it is run again on that branch name.

An op of an unknown type or version is skipped, so an older git-zf reads refs
written by a newer one.

## Sharing

A command that writes an op pushes it right away, with a plain fast-forward
push, never forced. When the push fails the op stays local and goes out with
the next sync. `git zf issue start` pushes the chain, not the branch: until
your first `git push`, the remote knows the branch is in progress and does not
have it.

The commands that offer a choice of branches (`issue close`, `review request`,
`review sync`, `branch prune`) fetch `refs/zf/branches/*` first. `branch list`
and `issue list` do not contact the remote: they show what the last fetch
brought.

Run `git zf init` once per clone. It adds the fetch refspec to every remote, so
a plain `git fetch` brings the chains, and `git fetch --prune` does not delete
their tracking refs.

Because the chains are shared, `git zf branch list` shows the branches of the
whole team. The pickers that act on a checked-out branch only offer the
branches that exist in your clone; `issue close` also offers a branch that
exists only on the remote, and checks it out for the merge.

## Pruning

`git zf branch prune` never deletes anything. For each branch in progress:

| The branch | Prune records |
|---|---|
| exists locally and is merged into the base | `merged` |
| is gone locally and on the remote, and you started it | `closed` |
| is gone locally and on the remote, and someone else started it | nothing; it is listed as skipped. With `--others`: `closed` |
| is gone locally and still on the remote | nothing |

"You started it" means your git identity is the author of its `start` op. A
branch someone else started and that exists nowhere you can see may be work
they have not pushed yet, or a branch they deleted and will close themselves.
When the remote cannot be reached, nothing is closed.

The identity is the exact `Name <email>` of your git configuration. Two kinds
of branch are therefore never closed by a plain prune: the ones of someone who
left or never prunes, and your own after you changed your name or email.
`git zf branch prune --others` closes those too. Use it when you know the
branches are abandoned: the summary names who started each one before asking
for confirmation.

To close one branch rather than every candidate, name it:
`git zf branch close <branch-name>`. It records that branch as closed whoever
started it and wherever it exists, and touches no git branch.

A close is not final. Checking the branch out and running `git zf issue track`
puts it back in progress.

## Upgrading from the SQLite store

Before branch refs were chains, git-zf kept branches in `.git/git-zf.db` and
wrote a JSON blob at `refs/zf/branches/<slug>`. Neither is migrated.

- Upgrade every clone of a repository together. An older git-zf does not see a
  chain, and its `issue start` force-pushes a blob over the one on the remote.
  If that happens, a push says so and names the command that deletes the blob:
  `git push <remote> --delete refs/zf/branches/<slug>`.
- After the upgrade no branch is tracked. For each branch still in progress,
  check it out and run `git zf issue track`. When the ref is still a blob, its
  parent, tracker type and issue ID are carried over to the new chain. The
  history of merged and closed branches is not.
- `issue close` and `review sync` refuse a sub-task whose parent is still a
  blob: track the parent's branch first.
- Blobs you never track again are reported in one warning by every listing.
  To drop the local ones:

  ```
  git for-each-ref --format='%(objecttype) %(refname)' refs/zf/branches |
    awk '$1 == "blob" { print $2 }' | xargs -r -n1 git update-ref -d
  ```

- Re-run `git zf init`: it adds the new fetch refspec.
- `.git/git-zf.db` is no longer read and can be deleted. The history of the
  commit form (`ctrl+r`) starts empty; it now lives in
  `.git/git-zf-history.jsonl`.
