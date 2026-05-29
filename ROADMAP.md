# Roadmap

## Enhancement

### End-to-end testability of interactive flows

Low priority (manual testing is fine, or surface is too small): `cmd/commit/commit.go` (thin wrapper around `commitpkg.FillOutForm`, already tested), `cmd/config/init.go` (config-file ops with low blast radius), `cmd/issue/issue.go` (top-level action dispatcher).

### Open: `git zf branch merge`

Still a placeholder (`cmd/branch/branch.go:mergeRunE`). The original bug-section note about the `issue close` UX ("it is not an issue, tell the user to use `git zf branch merge`") is the design seam: `branch merge` should take over the merge surface for non-issue branches, and `issue close` should redirect when invoked on one.
