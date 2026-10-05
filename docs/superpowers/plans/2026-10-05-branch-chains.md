# Plan: branch refs as commit chains, SQLite removed

**Spec:** `docs/superpowers/specs/2026-10-05-branch-chains-design.md`

Each task leaves `mise exec -- go build ./...` green. Tests are ported with the
code they cover; the full suite is green again at the end of task 6.

## 1. `git/`: plumbing

- `BranchRefs = ChainRefs{name: "branches"}`; `ConfigureChainFetch` adds its
  refspec.
- `ReadAllChains(ctx, ns)`: `for-each-ref`, one `git log --stdin`, one
  `cat-file --batch`; chains rebuilt by walking parents.
- `DeleteChainRef(ctx, ns, id, remoteBlob)`: leased remote delete, skipped when
  `remoteBlob` is empty. `LegacyBlob(ctx, ns, id)` returns the local blob's
  JSON and the blob SHA the tracking ref holds.
- `FetchChains`, `PushChain`, `SyncChains`: the bodies of `review.Fetch`,
  `Push` and `Sync`, taking the namespace and the merge payload.
- Tests: `git/chain_ref_test.go` (`ReadAllChains` against `ReadChainCommits`,
  the leased delete, the refspec).

## 2. `branch/`: fold and glue

- `record.go`: `Op`, `Entry`, `State`, `Row`, statuses, `DecodeOp`, `Fold`,
  `Rows`.
- `repo.go`: `Load`, `List`, `Find`, `Start`, `SetStatus`, `Fetch`, `Push`,
  `Sync`, `ErrLegacyBranch`.
- `branchtest.Seed`.
- `review/repo.go` calls the shared `git` helpers; `review.List` uses
  `ReadAllChains`.
- Tests: `branch/record_test.go` (`TestFold` table), `branch/repo_test.go`.

## 3. View types leave `store/`

- `store.BranchRow` → `branch.Row`; `store.BranchStatus*` → `branch.Status*`.
- `store.IssueRow` and the helpers of `store/helpers.go` → `issue/row.go`.
- `tui/`, `tty/` and their tests follow.

## 4. Call sites

In this order, each with its tests:

1. `cmd/issueflow`: `start.go`, `parent.go`, `closecandidates.go`,
   `review_guard.go`; `reconcile.go` deleted.
2. `cmd/issue`: `close.go`, `list.go`, `record.go`.
3. `cmd/branch`: `branch.go` (list, prune with the new rules), `prune_tracker.go`,
   `merge.go`.
4. `cmd/review`: `deps.go` (no store), `request.go`, `sync.go`, `status_cmd.go`,
   `guard.go`, `track.go`, `tracker.go`.
5. `cmd/commit`, `commit/`: `branch.Find`; history file (`commit/history.go`).
6. `cmd/init`: third refspec, the `git-zf.db` notice.

## 5. Delete

- `store/`, `git/branch_ref.go`, most of `git/blob_ref.go`,
  `cmd/issueflow/reconcile.go`.
- `go mod tidy`: `modernc.org/sqlite` and its transitive dependencies go.

## 6. New E2E cases

- Close of a sub-task whose parent ref is a blob is refused.
- Prune: a chain with no branch anywhere, started by someone else, is skipped;
  nothing is closed when the fetch fails.
- `branch list` shows a branch started in another clone.
- Two clones holding the same legacy blob (second appends; both offline merge;
  the lease spares a chain pushed in between).

## 7. Documentation

- `docs/branch-refs.md`; README (store wording, upgrade note); `CLAUDE.md`
  testing sections and architecture list; the `issue start` help text.

## Verification

    mise exec -- go build ./... && mise exec -- go vet ./...
    mise exec -- go test ./...
    mise exec -- golangci-lint run --new-from-rev HEAD ./...
    grep -rn "git-zf/store\|sqlite" --include=*.go . ; grep -n sqlite go.mod   # both empty
