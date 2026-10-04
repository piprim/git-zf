# Standard commit messages include the issue title

**Date:** 2026-07-05
**Status:** Approved (design), pending implementation

## Problem

`git zf issue close` prefills the commit form with the ticket title
(`IssueHint.IssueSubject`, sourced from `pickedBranch.Title`), so close
commits carry the issue title. The standard `git zf commit` flow builds its
hint from the branch name alone (`issueHintFromClient` in
`cmd/commit/commit.go`) and never populates `IssueSubject`, so the
already-implemented body prefill in `prefillNotClosed`
(`commit/form.go`) — `body = "# <IssueSubject>"` — is unreachable from
`git zf commit`.

## Decisions

- **Placement: body.** The title is prefilled into the body as
  `# <issue title>`. The subject stays free for describing the specific
  change; the close flow keeps its subject-based behavior. Example result:

  ```
  fix(ABC-1): handle nil branch ref

  # Add OAuth login

  Refs #ABC-1
  ```

- **Review branches included.** Branches named `<issue>@review` get the
  same body prefill; `issueHintFromClient` already extracts their issue ID.

- **Missing title is silent.** If the store has no row for the issue
  (hand-made branch, fresh clone without the store) or the lookup errors,
  `IssueSubject` stays empty, the body prefill is skipped by the existing
  `h.IssueSubject != ""` guard, and the commit proceeds. Lookup failures are
  logged at debug level only.

- **Approach A: enrich the hint in `runE`.** `issueHintFromClient` remains
  a pure branch-name parser (keeps its fake-`Committer` unit-test seam).
  The title lookup happens in `runE`, where the store is already open.
  Rejected alternatives: passing the store into `issueHintFromClient`
  (changes its role, forces a store fake into its tests) and resolving the
  title inside the `commit` package (the only other caller, the close flow,
  already has the title on `pickedBranch.Title`).

## Data flow

1. `git zf commit` on branch `ABC-1@feat@add-thing` or `ABC-1@review`.
2. `issueHintFromClient(client)` parses the branch name → `IssueHint{IssueID,
   BranchType}` (unchanged).
3. `store.OpenRepo` opens the store (already in `runE`).
4. **New:** `hint.IssueSubject = issueTitleFromStore(cmd.Context(), s,
   hint.IssueID)`.
5. `hint.Prefill(cfg.CommitMessage)` → `prefillNotClosed` emits
   `body = "# <title>"` when the config has a body item (existing code,
   already tested).

## New code

One unexported helper in `cmd/commit/commit.go` (~15 lines) plus one
assignment line in `runE`:

```go
// issueTitleFromStore returns the stored issue title for slug, or "" when
// slug is empty, no row matches, or the lookup fails — a missing title only
// skips the body prefill and must never block a commit.
func issueTitleFromStore(ctx context.Context, s *store.Store, slug string) string {
	if slug == "" {
		return ""
	}

	rows, err := s.ListBranchesByIssueSlugs(ctx, []string{slug})
	if err != nil {
		slog.Debug("could not look up issue title", "slug", slug, "error", err)

		return ""
	}

	return rows[slug].Title // missing key → zero row → ""
}
```

Not touched: the `Committer` interface, `issueHintFromClient`, the `commit`
package, the close flow.

## Edge cases

| Situation | Behavior |
|---|---|
| Non-issue branch / detached HEAD | `IssueID` empty → no lookup, identical to today |
| Store row missing or query error | Empty title → body prefill skipped, debug log only |
| Config without a `body` item | Title dropped by `prefillNotClosed` (existing guard) |
| Config with `body` but no `footer`/`subject` | Body becomes `"# <title>\n\nRefs #ABC-1"` (existing ref-fallback behavior, already tested) |
| Close flow | Unchanged — title already sourced from `pickedBranch.Title` |

## Testing

- New `TestIssueTitleFromStore` in `cmd/commit`, following the
  `commit_merge_parent_test.go` pattern (temp dir + `store.Open` +
  `InsertIssueWithBranch`), with `t.Run` subtests: title found, slug with no
  row, empty slug (no store call), lookup error (closed store).
- Prefill rendering is already covered in `commit/form_test.go`
  (`body_gets_issue_subject_when_present`,
  `body_gets_subject_then_ref_when_only_body_present`); no new tests there.

## Out of scope

- Prefilling the title into the subject or footer of standard commits.
- Falling back to the tracker API or de-slugifying the branch name when the
  store has no row.
- Any change to the close-flow message composition.
