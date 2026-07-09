package branch

import "strings"

const reviewSuffix = "@review"

// ReviewBranchName returns the review branch name for an issue slug:
// "<issueSlug>@review".
func ReviewBranchName(issueSlug string) string { return issueSlug + reviewSuffix }

// IsReviewBranch reports whether name is a review branch ("<slug>@review").
func IsReviewBranch(name string) bool { return strings.HasSuffix(name, reviewSuffix) }

// CutReviewSuffix returns the issue slug for a review branch name, and
// whether name was a review branch.
func CutReviewSuffix(name string) (string, bool) { return strings.CutSuffix(name, reviewSuffix) }
