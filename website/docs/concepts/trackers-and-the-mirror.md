---
sidebar_position: 3
title: Trackers and the mirror
---

After this page you know what changes when a tracker is configured, what
the mirror does, and which commands talk to the tracker.

## Two modes

**Without a tracker**, issues live in the repository under `refs/zf/issues/`
(see [where git-zf keeps its state](state-in-the-repository.md)).
`issue list` shows them with the tracked branches.

**With a tracker**, `issue start`, `issue list`, `issue close`,
`branch prune-tracker` and the review commands talk to **Redmine**,
**GitHub** or **Forgejo / Gitea**. The tracker is the primary source of
`issue list`, `issue start` can move an issue to "In Progress", and
`issue close` and the review decisions propose a status update, picked
from the tracker's live list of statuses.

```toml
[issue-tracker]
type     = "forgejo"                # "redmine", "github", "forgejo" or "gitea"
url      = "https://codeberg.org"
token    = "your_access_token"
mirror   = false                    # true: mirror this project's issues with the repository

[[issue-tracker.projects]]          # optional; one table per project
name = "myproject"                  # stable local name (lowercased), stored in the repository
id   = "owner/repo"                 # what the tracker calls it
```

The [configuration](../reference/configuration.md#issue-tracker) page
describes every key, the token scopes per tracker, and what the project
list changes. One thing to know up front: on GitHub, Forgejo and Gitea
exactly one project entry is required to update an issue's status, since
their issue endpoints are scoped to one repository.

When the tracker is unavailable or returns no issues, `issue start` falls
back to manual input.

## The mirror

With `mirror = true`, the issues of one tracker project are mirrored with
the issues stored in the repository. Title, description, labels and comments
travel both ways: an edit in the tracker's web UI shows up in `issue show`,
and `issue edit`, `issue label` and `issue comment` reach the tracker at
once. Open and closed travel both ways too.

- When both sides changed a title or a description between two syncs, the
  repository wins and one warning names the tracker's value.
- A label the tracker lacks is created there. `issue label` and `issue new`
  offer the tracker's labels.
- An issue you assign to yourself in `issue new` is assigned to your
  tracker account when the mirror creates it there. Assignments made later,
  or in the tracker's web UI, do not travel.
- On Redmine, which has no labels, the status is the one label:
  `issue label +Resolved -New` is a status change, and the form is a single
  pick.

After turning the mirror on, run `git zf issue sync` once before editing:
the first run pulls the tracker's content wherever it differs from the
repository.

## What reconciles, and what does not

With the mirror on, the repository issues are the mirror. Only the commands
that change something reconcile with the tracker: `issue sync`, the `r` key
of the issue table, the `issue start` picker (it must be fresh), and the
commands that create an issue or change its state. `issue list` and the
other read-only commands then never contact the tracker: they show what the
last sync brought. Without the mirror, `issue list` reads the tracker
directly. Two clones can import the same tracker issue offline; the
imported issue has the same ID in both, so they merge when they sync.

## An instance behind HTTP Basic auth

For a Forgejo or Gitea instance fronted by a reverse proxy that protects
the whole site, put the gate credentials in the URL:

```toml
url = "https://user:password@forgejo.example.org"
```

They are sent as `Authorization: Basic` for the proxy and the Forgejo token
is passed as the `token` query parameter instead, which Forgejo and Gitea
accept unless `[security] DISABLE_QUERY_AUTH_TOKEN = true` is set.

:::warning
A token in the query string can end up in the proxy's access logs.
:::

## Commands used

- [`git zf issue sync`](../reference/commands/git_zf_issue_sync.md)
- [`git zf issue start`](../reference/commands/git_zf_issue_start.md)
- [`git zf issue list`](../reference/commands/git_zf_issue_list.md)
- [`git zf issue label`](../reference/commands/git_zf_issue_label.md)
- [`git zf branch prune-tracker`](../reference/commands/git_zf_branch_prune-tracker.md)
