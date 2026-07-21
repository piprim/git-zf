package issueflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// CandidateStore is the slice of *store.Store the close-candidate helpers need.
type CandidateStore interface {
	ListBranches(ctx context.Context, status store.BranchStatus) ([]store.BranchRow, error)
	InsertIssueWithBranch(ctx context.Context, issue *store.Issue, b *store.Branch) error
}

// CandidateClient is the slice of *git.Client the close-candidate helpers need.
type CandidateClient interface {
	ListBranchRefs(ctx context.Context) ([]git.BranchRef, error)
	ReadBranchRef(ctx context.Context, issueSlug string) (*git.BranchRef, error)
	ResolveBranchRef(name string) (plumbing.Hash, error)
	BranchExists(name string) (bool, error)
	CreateLocalBranch(ctx context.Context, name, startPoint string) error
}

// CloseCandidates returns the branches a close picker should offer: every
// in-progress branch from the store, unioned with every refs/zf/branches/*
// entry (merged=false) that has no store row and whose feature branch resolves
// locally or via origin. Ref-derived rows carry IssueID == 0 as the
// "not yet tracked in this store" marker; MaterializeAndTrack promotes them.
//
// Reads local refs only — callers should FetchBranchRefs first (getPickedBranch
// does so via ReconcileMergedFromRefs). Mirrors the cross-machine ref fallback
// in ResolveParentBranch. A ListBranchRefs failure degrades to store-only
// candidates (never fatal to close).
func CloseCandidates(ctx context.Context, s CandidateStore, c CandidateClient) ([]store.BranchRow, error) {
	inProgress, err := s.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return nil, fmt.Errorf("list in-progress branches: %w", err)
	}

	seen := make(map[string]bool, len(inProgress))
	for _, b := range inProgress {
		seen[b.IssueSlug] = true
	}

	refs, err := c.ListBranchRefs(ctx)
	if err != nil {
		return inProgress, nil //nolint:nilerr // best-effort: degrade to store-only
	}

	out := inProgress
	for _, ref := range refs {
		if ref.Merged || seen[ref.IssueSlug] {
			continue
		}
		if _, resolveErr := c.ResolveBranchRef(ref.BranchName); resolveErr != nil {
			continue // feature branch not present locally or on origin — cannot close
		}
		seen[ref.IssueSlug] = true
		out = append(out, synthRow(ref))
	}

	return out, nil
}

// synthRow builds a BranchRow from a branch ref for an untracked candidate.
// IssueID is left 0 (marker). Type/Title come from the parsed branch name; on a
// parse failure Type is "" and Title falls back to the issue slug.
func synthRow(ref git.BranchRef) store.BranchRow {
	row := store.BranchRow{
		IssueID:    0,
		IssueSlug:  ref.IssueSlug,
		BranchName: ref.BranchName,
		Title:      ref.IssueSlug,
		Status:     store.BranchStatusInProgress,
	}
	if b, err := branch.Parse(ref.BranchName); err == nil {
		row.Type = b.Type()
		row.Title = strings.ReplaceAll(b.Title(), "-", " ")
	}
	if t, terr := time.Parse(time.RFC3339, ref.CreatedAt); terr == nil {
		row.CreatedAt = t
	}
	return row
}

// Compile-time checks that the production types satisfy the roles.
var (
	_ CandidateStore  = (*store.Store)(nil)
	_ CandidateClient = (*git.Client)(nil)
)

// MaterializeAndTrack promotes a ref-derived close candidate (IssueID == 0) into
// a tracked branch: it materializes the local feature branch from origin when
// absent, inserts the issue + branch rows, then returns the now-tracked
// BranchRow (with a real IssueID) for the rest of the close flow. A candidate
// that is already tracked (IssueID != 0) is returned unchanged.
//
// The inserted issue's TrackerType is read from the branch ref so
// `git zf issue list` classifies it correctly; the tracker-update gate in the
// close flow reads TrackerType from the ref + config, not this row, so an empty
// value here does not change tracker behavior.
func MaterializeAndTrack(
	ctx context.Context, s CandidateStore, c CandidateClient, picked store.BranchRow,
) (store.BranchRow, error) {
	if picked.IssueID != 0 {
		return picked, nil // already tracked; developer started it locally
	}

	// 1. Materialize the local feature branch from origin when it is absent.
	exists, err := c.BranchExists(picked.BranchName)
	if err != nil {
		return store.BranchRow{}, fmt.Errorf("check branch %q exists: %w", picked.BranchName, err)
	}
	if !exists {
		h, resolveErr := c.ResolveBranchRef(picked.BranchName)
		if resolveErr != nil {
			return store.BranchRow{}, fmt.Errorf("resolve feature branch %q: %w", picked.BranchName, resolveErr)
		}
		if createErr := c.CreateLocalBranch(ctx, picked.BranchName, h.String()); createErr != nil {
			return store.BranchRow{}, fmt.Errorf("materialize feature branch %q: %w", picked.BranchName, createErr)
		}
	}

	// 2. Auto-track (idempotent): skip the insert when the branch is already tracked.
	all, err := s.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return store.BranchRow{}, fmt.Errorf("list branches: %w", err)
	}
	if tracked := findByBranchName(all, picked.BranchName); tracked != nil {
		return *tracked, nil
	}

	// Carry the tracker origin from the ref so `git zf issue list` classifies the
	// issue correctly. Best-effort: a read miss degrades to nil (manual), which is
	// harmless — the tracker-update gate in updateClosedStatus reads the ref and
	// config, not this row.
	var trackerType *string
	if ref, _ := c.ReadBranchRef(ctx, picked.IssueSlug); ref != nil && ref.TrackerType != "" {
		tt := ref.TrackerType
		trackerType = &tt
	}
	if insErr := s.InsertIssueWithBranch(ctx,
		&store.Issue{
			IDSlug: picked.IssueSlug, Title: picked.Title,
			StatusID: store.StatusIDInProgress, TrackerType: trackerType,
		},
		&store.Branch{Name: picked.BranchName, Type: picked.Type, StatusID: store.StatusIDInProgress},
	); insErr != nil {
		return store.BranchRow{}, fmt.Errorf("track branch %q: %w", picked.BranchName, insErr)
	}

	// Re-read so IssueID is populated for downstream (updateClosedStatus).
	all, err = s.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return store.BranchRow{}, fmt.Errorf("re-list branches: %w", err)
	}
	tracked := findByBranchName(all, picked.BranchName)
	if tracked == nil {
		return store.BranchRow{}, fmt.Errorf("tracked branch %q not found after insert", picked.BranchName)
	}
	return *tracked, nil
}

// findByBranchName returns a pointer to the row whose BranchName matches, or nil.
func findByBranchName(rows []store.BranchRow, name string) *store.BranchRow {
	for i := range rows {
		if rows[i].BranchName == name {
			return &rows[i]
		}
	}
	return nil
}
