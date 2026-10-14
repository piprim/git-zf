# Roadmap

## Enhancement

1. Issue mirror, repo <-> tracker (first version shipped). Spec: docs/superpowers/specs/2026-10-06-issue-tracker-mirror-design.md. Left out of its first version:
  a. [abandoned] ~Several projects: project-aware tracker calls, project-qualified slugs, a project picker in `issue new`.~
  b. [done] Sync of title, description, labels and comments. Spec: docs/superpowers/specs/2026-10-10-issue-mirror-content-sync-design.md
  c. `issue start` should assign to me if it's not already assigned to me.
  d. A warning from the reconcile when two records link the same tracker number (one clone exported while another imported the new issue before fetching).
2. In-repo issue must carry on author and assignee: Who opened it and who works on it, from git identity. Lets `issue list` filter 'mine'. Comes after the issue mirror.
3. AI assistant. See https://github.com/rshdhere/vibecheck
4. git zf branch close <branch-name>
   No menu entry or picker. The command takes exactly one name and is not in the git zf branch menu. A picker over in-progress branches is a small addition
5. `git zf issue new` should propose to start the issue now (yes/no).
6. `git zf issue list` show the issues. Allow to pick one and open a view the same `git zf shwo <id>`
7. [done] `git zf issue` must show the list by default. The action `Close`, 'New', `Edit`, `Label` and `Sync` can be called throught key-binding.
8. `issue new` : the label entry is a free text form, it should be like the form of pressing `l` in `issue list`. The label form should be shared acrross all form.
9. les hints en bas ils sont peu visibles. Est-ce un problème de configuration du terminal ?
10. `git zf issue start` always make a synck when mirroring is on but `git zf issue` does not. `git zf issue start` should need an issueID, if missing the same TUI than `git zf issue` is opened

## To be discuss

