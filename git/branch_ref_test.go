package git

import (
	"testing"
)

func TestBranchRef_ReadWrite(t *testing.T) {
	t.Parallel()

	client, _ := newDiskRepo(t)

	ref := BranchRef{
		IssueSlug:  "X.1",
		BranchName: "X.1@feat@part-one",
		ParentSlug: "X",
		CreatedAt:  "2026-06-21T10:00:00Z",
	}

	t.Run("ReadBranchRef returns nil for non-existent ref", func(t *testing.T) {
		got, err := client.ReadBranchRef(t.Context(), "X.1")
		if err != nil {
			t.Fatalf("ReadBranchRef: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("WriteBranchRef creates ref", func(t *testing.T) {
		sha, err := client.WriteBranchRef(t.Context(), "X.1", ref)
		if err != nil {
			t.Fatalf("WriteBranchRef: %v", err)
		}
		if sha == "" {
			t.Fatal("expected non-empty SHA")
		}
	})

	t.Run("ReadBranchRef returns written ref", func(t *testing.T) {
		got, err := client.ReadBranchRef(t.Context(), "X.1")
		if err != nil {
			t.Fatalf("ReadBranchRef: %v", err)
		}
		if got == nil {
			t.Fatal("expected ref, got nil")
		}
		if got.IssueSlug != "X.1" {
			t.Errorf("IssueSlug: got %q, want %q", got.IssueSlug, "X.1")
		}
		if got.BranchName != "X.1@feat@part-one" {
			t.Errorf("BranchName: got %q, want %q", got.BranchName, "X.1@feat@part-one")
		}
		if got.ParentSlug != "X" {
			t.Errorf("ParentSlug: got %q, want %q", got.ParentSlug, "X")
		}
		if got.CreatedAt != "2026-06-21T10:00:00Z" {
			t.Errorf("CreatedAt: got %q, want %q", got.CreatedAt, "2026-06-21T10:00:00Z")
		}
	})

	t.Run("WriteBranchRef overwrites existing ref", func(t *testing.T) {
		updated := BranchRef{
			IssueSlug:  "X.1",
			BranchName: "X.1@feat@part-one",
			ParentSlug: "X",
			CreatedAt:  "2026-06-21T11:00:00Z",
		}
		_, err := client.WriteBranchRef(t.Context(), "X.1", updated)
		if err != nil {
			t.Fatalf("WriteBranchRef (overwrite): %v", err)
		}
		got, err := client.ReadBranchRef(t.Context(), "X.1")
		if err != nil {
			t.Fatalf("ReadBranchRef after overwrite: %v", err)
		}
		if got == nil {
			t.Fatal("expected ref after overwrite, got nil")
		}
		if got.CreatedAt != "2026-06-21T11:00:00Z" {
			t.Errorf("overwrite failed: CreatedAt = %q", got.CreatedAt)
		}
	})

	t.Run("TrackerType round-trips through write and read", func(t *testing.T) {
		trackerRef := BranchRef{
			IssueSlug:   "T.1",
			BranchName:  "T.1@feat@from-tracker",
			CreatedAt:   "2026-06-21T10:00:00Z",
			TrackerType: "fake",
		}
		if _, err := client.WriteBranchRef(t.Context(), "T.1", trackerRef); err != nil {
			t.Fatalf("WriteBranchRef: %v", err)
		}
		got, err := client.ReadBranchRef(t.Context(), "T.1")
		if err != nil {
			t.Fatalf("ReadBranchRef: %v", err)
		}
		if got == nil {
			t.Fatal("expected ref, got nil")
		}
		if got.TrackerType != "fake" {
			t.Errorf("TrackerType: got %q, want %q", got.TrackerType, "fake")
		}
	})

	t.Run("absent TrackerType reads back as empty", func(t *testing.T) {
		manualRef := BranchRef{
			IssueSlug:  "M.1",
			BranchName: "M.1@feat@manual",
			CreatedAt:  "2026-06-21T10:00:00Z",
		}
		if _, err := client.WriteBranchRef(t.Context(), "M.1", manualRef); err != nil {
			t.Fatalf("WriteBranchRef: %v", err)
		}
		got, err := client.ReadBranchRef(t.Context(), "M.1")
		if err != nil {
			t.Fatalf("ReadBranchRef: %v", err)
		}
		if got.TrackerType != "" {
			t.Errorf("TrackerType: got %q, want empty", got.TrackerType)
		}
	})

	t.Run("ref without parent slug is valid", func(t *testing.T) {
		rootRef := BranchRef{
			IssueSlug:  "X",
			BranchName: "X@feat@big-feature",
			CreatedAt:  "2026-06-21T10:00:00Z",
		}
		if _, err := client.WriteBranchRef(t.Context(), "X", rootRef); err != nil {
			t.Fatalf("WriteBranchRef root: %v", err)
		}
		got, err := client.ReadBranchRef(t.Context(), "X")
		if err != nil {
			t.Fatalf("ReadBranchRef root: %v", err)
		}
		if got.ParentSlug != "" {
			t.Errorf("ParentSlug: got %q, want empty", got.ParentSlug)
		}
	})
}

func TestListBranchRefs(t *testing.T) {
	t.Parallel()

	client, _ := newDiskRepo(t)

	t.Run("empty namespace returns empty slice", func(t *testing.T) {
		got, err := client.ListBranchRefs(t.Context())
		if err != nil {
			t.Fatalf("ListBranchRefs: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 refs, got %d: %+v", len(got), got)
		}
	})

	t.Run("returns every written ref", func(t *testing.T) {
		if _, err := client.WriteBranchRef(t.Context(), "X.1", BranchRef{
			IssueSlug: "X.1", BranchName: "X.1@feat@one", CreatedAt: "2026-07-21T10:00:00Z",
		}); err != nil {
			t.Fatalf("WriteBranchRef X.1: %v", err)
		}
		if _, err := client.WriteBranchRef(t.Context(), "X.2", BranchRef{
			IssueSlug: "X.2", BranchName: "X.2@fix@two", Merged: true, TrackerType: "github",
		}); err != nil {
			t.Fatalf("WriteBranchRef X.2: %v", err)
		}

		got, err := client.ListBranchRefs(t.Context())
		if err != nil {
			t.Fatalf("ListBranchRefs: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 refs, got %d: %+v", len(got), got)
		}

		bySlug := map[string]BranchRef{}
		for _, r := range got {
			bySlug[r.IssueSlug] = r
		}
		if bySlug["X.1"].BranchName != "X.1@feat@one" {
			t.Errorf("X.1 BranchName = %q, want X.1@feat@one", bySlug["X.1"].BranchName)
		}
		if !bySlug["X.2"].Merged || bySlug["X.2"].TrackerType != "github" {
			t.Errorf("X.2 = %+v, want Merged=true TrackerType=github", bySlug["X.2"])
		}
	})
}
