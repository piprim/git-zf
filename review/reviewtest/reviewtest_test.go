package reviewtest_test

import (
	"os/exec"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/review/reviewtest"
)

func TestSeed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "t"},
		{"config", "user.email", "t@test.com"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	c, err := git.NewClientAt(nil, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	for name, tc := range map[string]struct {
		slug, status string
		round        int
	}{
		"in review, round 1":         {"a", review.StatusInReview, 1},
		"approved, round 1":          {"b", review.StatusApproved, 1},
		"changes requested, round 3": {"c", review.StatusChangesRequested, 3},
	} {
		t.Run(name, func(t *testing.T) {
			reviewtest.Seed(t, c, tc.slug, tc.status, tc.round, "f1")

			st, err := review.Load(t.Context(), c, tc.slug)
			if err != nil || st == nil {
				t.Fatalf("Load = %v, %v", st, err)
			}
			if st.Status != tc.status || st.Round != tc.round || st.FeatureSHA != "f1" {
				t.Errorf("state = %+v", st)
			}
		})
	}
}
