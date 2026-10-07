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
| `create` | `title`, `description`, `branch_type`; for an issue imported from the tracker also `tracker_type`, `project`, `tracker_id` | First commit of the chain. |
| `set_title` | `value` | Replaces the title. Written by `git zf issue edit`. |
| `set_description` | `value` | Replaces the description; no `value` clears it. Written by `git zf issue edit`. |
| `set_state` | `value`: `open` or `closed` | Opens or closes the issue. |
| `add_label` | `value` | Adds a label. |
| `remove_label` | `value` | Removes a label; nothing happens if it is absent. |
| `add_comment` | `body` | Adds a comment. |
| `link_tracker` | `tracker_type`, `project`, `tracker_id` | The tracker issue a repo-born issue was created as. The first one wins. |
| `tracker_state` | `value`: `open` or `closed`, `status` | The last tracker state the chain saw. |
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

## Tracker mirror

With `mirror = true` under `[issue-tracker]` and exactly one
`[[issue-tracker.projects]]` entry, the issues of that tracker project and the
issues of the repository are one set:

- every open tracker issue is imported as an issue ref;
- every open repo-born issue is created in the tracker;
- open/closed travels both ways.

`git zf issue list`, `issue new`, `issue close <id>`, `issue close` (merge)
and `issue sync` reconcile. `show`, `edit`, `comment` and `label` only read
the refs: they see what another clone reconciled.

### Identity of an imported issue

The root commit of an imported issue is built from four values only: the
tracker type, the project's near slug, the tracker's issue number and its
creation date. Author, committer and dates are fixed and the commit is never
signed, so two clones importing the same tracker issue write the same commit
and therefore the same ref. Title and description follow as ordinary ops.

The near slug is part of the identity; the far slug is not. Renaming the
project in the tracker means editing `far-slug`. Changing `near-slug` imports
every issue again under new IDs.

### Which side moved

Each reconcile compares the repo state, the tracker state, and the tracker
state the chain last recorded (`tracker_state`). The side that differs from
the recorded one moved, and the other follows. With two values, both sides
moving means they agree.

### Display

An imported issue is shown, and names its branches, by its tracker number. A
repo-born issue keeps its short hash; `issue list` shows `a1b2c3d (#57)` once
it is exported. `issue show 57` finds either.

### Limits

- Two clones exporting the same issue at the same moment create two tracker
  issues. One link wins; the other issue is reported on each reconcile until
  it is closed in the tracker.
- The project's open issues are listed in full on each reconcile.
- Issues already closed in the tracker, or closed in the repository before
  the mirror was on, are not mirrored.
- A close learned by asking the tracker (the issue left the open listing)
  records the status name `closed`. On Redmine the real name ("Rejected",
  "Closed") is only read from a listing, which no longer shows the issue, so
  the status column of such an issue reads `closed`.
- One clone exporting an issue while another clone reconciles before fetching
  that push imports the new tracker issue as a second, tracker-born record.
  Both then link the same number; close one of them.
