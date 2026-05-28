# Roadmap

## Enhancement

### End-to-end testability of interactive flows

Low priority (manual testing is fine, or surface is too small): `cmd/commit/commit.go` (thin wrapper around `commitpkg.FillOutForm`, already tested), `cmd/config/init.go` (config-file ops with low blast radius), `cmd/issue/issue.go` (top-level action dispatcher).

## Bug

1. Ending the closing an issue, the title `fmt.Sprintf("Update issue %s status
   in %s:", issueID, trackerType)` does no appear
2. >  git zf issue close
   Already up to date.
   time=2026-05-28T16:21:13.995+02:00 level=WARN msg="could not load author list" error="walk commits: malformed idx file: packfile mismatch: target is \"aa9987d4cf29bd8b3e164ebd1936eaedb9b39eb1\" not \"db6d3423c1465cd6f8ffc9691880e0df78ead011\""
       ┃ Author:
       ┃ > (no authors found)
3. the current-identity prepend at git/git.go:282 uses
   ConfigScoped(config.SystemScope), which only reads /etc/gitconfig — so for
   users whose identity lives in ~/.gitconfig or the repo's local config (most
   setups), it never fires.

### Open: `git zf branch merge`

Still a placeholder (`cmd/branch/branch.go:mergeRunE`). The original bug-section note about the `issue close` UX ("it is not an issue, tell the user to use `git zf branch merge`") is the design seam: `branch merge` should take over the merge surface for non-issue branches, and `issue close` should redirect when invoked on one.
