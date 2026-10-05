# Review refs

git-zf stores the review of an issue in the repository as a chain of commits
under `refs/zf/reviews/<slug>`, where `<slug>` is the issue's ID as it appears
in the branch name. This page describes the format, for people reading it with
plain git:

```
git log refs/zf/reviews/<slug>
git show <commit>:op.json
```

## Layout

| Ref | Content |
|---|---|
| `refs/zf/reviews/<slug>` | The local review. |
| `refs/remotes/<remote>/zf/reviews/<slug>` | What the remote had at the last fetch or push. Never edited by hand. |

A review ref is never deleted. When the issue is closed, the chain gets a
`close` op and stays as a record of who approved what.

## Ops

Every action is one commit. Its tree holds one file, `op.json`; its parent is
the previous action. Author and committer are the user's git identity.

```json
{"v":1,"type":"approve","at":"2026-10-04T10:00:00Z","author":"Pi <pi@example.org>","approved_sha":"3fad482…","round":1}
```

| `type` | Fields | Written by | Effect |
|---|---|---|---|
| `request` | `feature_sha` | `git zf review request` | Starts a round: the branch is locked. First commit of the chain on round 1. |
| `start` | `round` | `git zf review start` | Records the reviewer. |
| `approve` | `approved_sha`, `has_commits`, `round` | `git zf review approve` | Approves the commit `approved_sha`. |
| `reject` | `comment`, `has_commits`, `round` | `git zf review reject` | Requests changes: the branch is unlocked. |
| `close` | none | `git zf issue close` | The issue was merged. |
| `merge` | none | sync | Two-parent commit joining actions made on two clones. |

`v` is the format version, currently 1. `at` is RFC 3339, UTC. `round` is the
round the op was written for; an op without it applies to the current round.

## Reading a review

The current state is the fold of all ops: a commit is applied after its
parents; commits with no order between them (made on two clones before either
synced) are applied by `at`, then by commit ID. An op that does not fit the
current status is ignored, which settles concurrent actions the same way on
every clone:

- two requests made at once count as one round;
- two approvals are both kept;
- an approval and a rejection made at once end as changes requested;
- two rejections keep both comments;
- an approval, rejection or start written for an earlier round is ignored.

An op of an unknown type or version is skipped, so an older git-zf reads refs
written by a newer one.

## Sharing

Every review command fetches `refs/zf/reviews/*` into the tracking namespace,
then for each review: creates the local ref if it is missing, fast-forwards it
if it is behind, leaves it if it is ahead, and writes a `merge` commit if both
sides changed. Pushes are plain fast-forward pushes, never forced, so a push
cannot discard someone else's action. A command that writes an op pushes it
right away; when the push fails the op stays local and goes out with the next
review command.

Run `git zf init` once per clone. It adds the fetch refspec to every remote, so a
plain `git fetch` brings the reviews, and `git fetch --prune` does not delete
their tracking refs. The tracking refs appear in `git branch -r` as
`<remote>/zf/reviews/<slug>`. A remote branch named exactly `zf` conflicts with
these tracking refs and makes `git fetch` fail.

## Signed approvals

An op is signed when `commit.gpgsign` is true. To require it, set in
`.git-zf.toml`:

```toml
[review]
require-signed = true
```

The file is per clone, so each side sets it:

- on the reviewer's clone, `git zf review approve` signs the approval and
  fails if it cannot;
- on the developer's clone, `git zf issue close` refuses to merge unless an
  approval of the current round verifies with `git verify-commit` and its
  `approved_sha` is exactly the commit to be merged. A commit added after the
  approval is not covered: request a new round. With the flag set,
  `git zf review request` offers an approved review for that purpose.

`git zf review status` shows each approval as `verified` (with the signer git
reports), `signed, not verified` or `unsigned`. Trust is git's own: the GPG
keyring, or `gpg.ssh.allowedSignersFile` for SSH signatures. git-zf does not
check who the signer is. With GPG, any key in the keyring verifies unless
`gpg.minTrustLevel` is raised.

## Reviews written by an older git-zf

Before this format a review was a JSON blob at the same ref name. Such a ref is
not migrated. `git zf review request` replaces it; `git zf issue close` refuses
to merge while it exists and prints the command to delete it.
