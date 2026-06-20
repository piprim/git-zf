package git

import (
	"testing"
)

func TestReviewRef_ReadWriteDelete(t *testing.T) {
	t.Parallel()

	client, _ := newDiskRepo(t)

	ref := ReviewRef{
		Status:     "in_review",
		Round:      1,
		FeatureSHA: "abc1234def5678",
		CreatedAt:  "2026-06-20T10:00:00Z",
	}

	t.Run("ReadReviewRef returns nil for non-existent ref", func(t *testing.T) {
		got, sha, err := client.ReadReviewRef(t.Context(), "42")
		if err != nil {
			t.Fatalf("ReadReviewRef: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil ref, got %+v", got)
		}
		if sha != "" {
			t.Errorf("expected empty SHA, got %q", sha)
		}
	})

	var writtenSHA string

	t.Run("WriteReviewRef creates ref when oldSHA is empty", func(t *testing.T) {
		sha, err := client.WriteReviewRef(t.Context(), "42", ref, "")
		if err != nil {
			t.Fatalf("WriteReviewRef: %v", err)
		}
		if sha == "" {
			t.Fatal("expected non-empty SHA")
		}
		writtenSHA = sha
	})

	t.Run("ReadReviewRef returns written ref", func(t *testing.T) {
		got, sha, err := client.ReadReviewRef(t.Context(), "42")
		if err != nil {
			t.Fatalf("ReadReviewRef: %v", err)
		}
		if got == nil {
			t.Fatal("expected ref, got nil")
		}
		if got.Status != "in_review" {
			t.Errorf("Status: got %q, want %q", got.Status, "in_review")
		}
		if got.Round != 1 {
			t.Errorf("Round: got %d, want 1", got.Round)
		}
		if got.FeatureSHA != "abc1234def5678" {
			t.Errorf("FeatureSHA: got %q, want %q", got.FeatureSHA, "abc1234def5678")
		}
		if sha != writtenSHA {
			t.Errorf("SHA mismatch: got %q, want %q", sha, writtenSHA)
		}
	})

	t.Run("WriteReviewRef with wrong oldSHA fails (CAS)", func(t *testing.T) {
		updated := ReviewRef{Status: "approved", Round: 1, FeatureSHA: "abc1234def5678", CreatedAt: "2026-06-20T11:00:00Z"}
		_, err := client.WriteReviewRef(t.Context(), "42", updated, "0000000000000000000000000000000000000000")
		if err == nil {
			t.Error("expected CAS failure with wrong oldSHA, got nil error")
		}
	})

	var updatedSHA string

	t.Run("WriteReviewRef with correct oldSHA updates ref", func(t *testing.T) {
		updated := ReviewRef{Status: "approved", Round: 1, FeatureSHA: "abc1234def5678", CreatedAt: "2026-06-20T11:00:00Z"}
		sha, err := client.WriteReviewRef(t.Context(), "42", updated, writtenSHA)
		if err != nil {
			t.Fatalf("WriteReviewRef update: %v", err)
		}
		if sha == writtenSHA {
			t.Error("updated SHA should differ from original SHA")
		}
		updatedSHA = sha
	})

	t.Run("ReadReviewRef returns updated status", func(t *testing.T) {
		got, sha, err := client.ReadReviewRef(t.Context(), "42")
		if err != nil {
			t.Fatalf("ReadReviewRef after update: %v", err)
		}
		if got == nil {
			t.Fatal("expected ref after update, got nil")
		}
		if got.Status != "approved" {
			t.Errorf("Status: got %q, want %q", got.Status, "approved")
		}
		if sha != updatedSHA {
			t.Errorf("SHA: got %q, want %q", sha, updatedSHA)
		}
	})

	t.Run("DeleteReviewRef removes local ref", func(t *testing.T) {
		if err := client.DeleteReviewRef(t.Context(), "42"); err != nil {
			t.Fatalf("DeleteReviewRef: %v", err)
		}
		got, _, err := client.ReadReviewRef(t.Context(), "42")
		if err != nil {
			t.Fatalf("ReadReviewRef after delete: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil after delete, got %+v", got)
		}
	})
}

func TestFetchReviewRefs_NoOpWithoutRemote(t *testing.T) {
	t.Parallel()

	client, _ := newDiskRepo(t)

	t.Run("FetchReviewRefs is a no-op when no remote configured", func(t *testing.T) {
		if err := client.FetchReviewRefs(t.Context()); err != nil {
			t.Errorf("FetchReviewRefs with no remote: %v", err)
		}
	})
}

func TestPushReviewRef_NoOpWithoutRemote(t *testing.T) {
	t.Parallel()

	client, _ := newDiskRepo(t)

	// Write a ref first so there's something to push (though push is no-op without remote).
	ref := ReviewRef{Status: "in_review", Round: 1, FeatureSHA: "abc123", CreatedAt: "2026-06-20T10:00:00Z"}
	if _, err := client.WriteReviewRef(t.Context(), "55", ref, ""); err != nil {
		t.Fatalf("WriteReviewRef: %v", err)
	}

	t.Run("PushReviewRef is a no-op when no remote configured", func(t *testing.T) {
		if err := client.PushReviewRef(t.Context(), "55", ""); err != nil {
			t.Errorf("PushReviewRef with no remote: %v", err)
		}
	})
}
