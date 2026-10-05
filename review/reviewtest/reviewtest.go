// Package reviewtest seeds review chains in the tests of other packages.
package reviewtest

import (
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/review"
)

// Seed brings the review of slug to status at the given round, as the review
// commands would: every earlier round is a request followed by a reject.
// status is review.StatusInReview, StatusApproved or StatusChangesRequested.
// The approval, if any, covers featureSHA. Nothing is pushed. The git client
// caches its remote name: add the remote before seeding when a test needs
// tracking behavior.
func Seed(t testing.TB, c *git.Client, slug, status string, round int, featureSHA string) {
	t.Helper()

	add := func(op review.Op) {
		t.Helper()

		if err := review.Append(t.Context(), c, slug, &op, false); err != nil {
			t.Fatalf("seed review %s: %s op: %v", slug, op.Type, err)
		}
	}

	for r := range max(round, 1) - 1 {
		add(review.Op{Type: review.OpRequest, FeatureSHA: featureSHA})
		add(review.Op{Type: review.OpReject, Round: r + 1})
	}

	add(review.Op{Type: review.OpRequest, FeatureSHA: featureSHA})

	switch status {
	case review.StatusApproved:
		add(review.Op{Type: review.OpApprove, ApprovedSHA: featureSHA, Round: max(round, 1)})
	case review.StatusChangesRequested:
		add(review.Op{Type: review.OpReject, Round: max(round, 1)})
	}
}
