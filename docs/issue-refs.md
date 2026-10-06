# Issue refs

git-zf stores each issue in the repository as a chain of commits under
`refs/zf/issues/<id>`. This page describes the format, for people reading it
with plain git:

```
git log refs/zf/issues/<id>
git show <commit>:op.json
```

## Layout

| Ref | Content |
|---|---|
| `refs/zf/issues/<id>` | The local issue. `<id>` is the full object ID of the issue's first commit. |
| `refs/remotes/<remote>/zf/issues/<id>` | What the remote had at the last fetch or push. Never edited by hand. |

The 7-character ID shown by `git zf issue list` and used in branch names is the
start of `<id>`. Commands accept the full ID or any unique prefix of at least 4
characters.

## Ops

Every change is one commit. Its tree holds one file, `op.json`; its parent is
the previous change. Author and committer are the user's git identity, and the
commit is signed when `commit.gpgsign` is true.

```json
{"v":1,"type":"add_comment","at":"2026-10-03T10:00:00Z","author":"Pi <pi@example.org>","body":"Reproduced on main."}
```

| `type` | Fields | Effect |
|---|---|---|
| `create` | `title`, `description`, `branch_type` | First commit of the chain. |
| `set_title` | `value` | Replaces the title. Written by `git zf issue edit`. |
| `set_description` | `value` | Replaces the description; no `value` clears it. Written by `git zf issue edit`. |
| `set_state` | `value`: `open` or `closed` | Opens or closes the issue. |
| `add_label` | `value` | Adds a label. |
| `remove_label` | `value` | Removes a label; nothing happens if it is absent. |
| `add_comment` | `body` | Adds a comment. |
| `merge` | none | Two-parent commit joining changes made on two clones. |

`v` is the format version, currently 1. `at` is RFC 3339, UTC.

## Reading an issue

The current state is the fold of all ops: a commit is applied after its
parents; commits with no order between them (made on two clones before either
synced) are applied by `at`, then by commit ID. The last `set_title`, `set_description` and
`set_state` win, labels form a set, comments accumulate. An op of an unknown type or version is
skipped, so an older git-zf reads refs written by a newer one.

## Sharing

`git zf issue sync` fetches `refs/zf/issues/*` into `refs/remotes/<remote>/zf/issues/*`,
then for each issue:

- no local ref: the local ref is created;
- local behind: it is fast-forwarded;
- local ahead: nothing;
- both changed: a `merge` commit with both tips as parents is written.

Pushes are plain fast-forward pushes, never forced, so a push cannot discard
someone else's ops. Every command that writes an op pushes it right away; when
the push fails the op stays local and goes out with the next sync.

A plain `git clone` does not bring these refs. Run `git zf init` once per
clone: it adds the fetch refspec to every remote, so that a plain `git fetch`
brings the issues and `git fetch --prune` does not delete their tracking refs.
Without it, run `git zf issue sync`. The tracking refs appear in
`git branch -r` as `<remote>/zf/issues/<id>`.
