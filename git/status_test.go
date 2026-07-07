package git

import (
	"testing"
)

func TestParsePorcelainV2(t *testing.T) {
	t.Parallel()

	t.Run("staged modified (ordinary '1' line)", func(t *testing.T) {
		t.Parallel()

		raw := "1 M. N... 100644 100644 100644 aaaa bbbb cmd/commit/commit.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1", len(got))
		}
		if got[0].XY != "M " {
			t.Errorf("XY = %q, want %q", got[0].XY, "M ")
		}
		if got[0].Path != "cmd/commit/commit.go" {
			t.Errorf("Path = %q, want %q", got[0].Path, "cmd/commit/commit.go")
		}
	})

	t.Run("unstaged modified normalises leading dot to space", func(t *testing.T) {
		t.Parallel()

		raw := "1 .M N... 100644 100644 100644 aaaa bbbb commit/form.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 || got[0].XY != " M" || got[0].Path != "commit/form.go" {
			t.Fatalf("got %+v, want XY=%q Path=%q", got, " M", "commit/form.go")
		}
	})

	t.Run("staged and unstaged (MM)", func(t *testing.T) {
		t.Parallel()

		raw := "1 MM N... 100644 100644 100644 aaaa bbbb commit/form.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 || got[0].XY != "MM" {
			t.Fatalf("got %+v, want XY=%q", got, "MM")
		}
	})

	t.Run("added and deleted words", func(t *testing.T) {
		t.Parallel()

		raw := "1 A. N... 000000 100644 100644 0000 bbbb git/status.go\n" +
			"1 D. N... 100644 000000 000000 aaaa 0000 old/removed.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 2 {
			t.Fatalf("len(got) = %d, want 2", len(got))
		}
		if got[0].XY != "A " || got[0].Path != "git/status.go" {
			t.Errorf("got[0] = %+v", got[0])
		}
		if got[1].XY != "D " || got[1].Path != "old/removed.go" {
			t.Errorf("got[1] = %+v", got[1])
		}
	})

	t.Run("renamed '2' line populates Path and OrigPath", func(t *testing.T) {
		t.Parallel()

		raw := "2 R. N... 100644 100644 100644 aaaa bbbb R100 new/name.go\told/name.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1", len(got))
		}
		if got[0].XY != "R " {
			t.Errorf("XY = %q, want %q", got[0].XY, "R ")
		}
		if got[0].Path != "new/name.go" || got[0].OrigPath != "old/name.go" {
			t.Errorf("Path=%q OrigPath=%q, want %q / %q", got[0].Path, got[0].OrigPath, "new/name.go", "old/name.go")
		}
	})

	t.Run("untracked '?' line yields ?? and path", func(t *testing.T) {
		t.Parallel()

		raw := "? docs/notes.md\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 || got[0].XY != "??" || got[0].Path != "docs/notes.md" {
			t.Fatalf("got %+v, want XY=?? Path=docs/notes.md", got)
		}
	})

	t.Run("unmerged 'u' line yields XY and path", func(t *testing.T) {
		t.Parallel()

		raw := "u UU N... 100644 100644 100644 100644 aaaa bbbb cccc conflict.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 || got[0].XY != "UU" || got[0].Path != "conflict.go" {
			t.Fatalf("got %+v, want XY=UU Path=conflict.go", got)
		}
	})

	t.Run("ignored '!', header '#', and blank lines are skipped", func(t *testing.T) {
		t.Parallel()

		raw := "# branch.oid aaaa\n! build/artifact\n\n1 M. N... 100644 100644 100644 aaaa bbbb keep.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 || got[0].Path != "keep.go" {
			t.Fatalf("got %+v, want single entry keep.go", got)
		}
	})

	t.Run("empty input yields no entries", func(t *testing.T) {
		t.Parallel()

		if got := parsePorcelainV2(""); len(got) != 0 {
			t.Errorf("len(got) = %d, want 0", len(got))
		}
	})

	t.Run("malformed short line is skipped", func(t *testing.T) {
		t.Parallel()

		if got := parsePorcelainV2("1 M.\n"); len(got) != 0 {
			t.Errorf("len(got) = %d, want 0 (malformed skipped)", len(got))
		}
	})

	t.Run("path containing spaces is preserved (trailing field)", func(t *testing.T) {
		t.Parallel()

		raw := "1 M. N... 100644 100644 100644 aaaa bbbb cmd/my file.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 || got[0].Path != "cmd/my file.go" {
			t.Fatalf("got %+v, want single entry path %q", got, "cmd/my file.go")
		}
	})
}

func TestStatusEntries(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	// Staged new file.
	writeFile(t, dir, "added.go", "package main\n")
	runGitInDir(t, dir, "add", "added.go")

	// Tracked file modified in the worktree only (base.go exists from newDiskRepo).
	writeFile(t, dir, "base.go", "package main // changed\n")

	// Untracked file.
	writeFile(t, dir, "untracked.txt", "hello\n")

	entries, err := client.StatusEntries(t.Context())
	if err != nil {
		t.Fatalf("StatusEntries: %v", err)
	}

	byPath := make(map[string]StatusEntry, len(entries))
	for _, e := range entries {
		byPath[e.Path] = e
	}

	t.Run("staged new file has index-side code", func(t *testing.T) {
		e, ok := byPath["added.go"]
		if !ok {
			t.Fatalf("added.go missing from %+v", entries)
		}
		if e.XY[0] != 'A' {
			t.Errorf("added.go XY = %q, want index-side 'A'", e.XY)
		}
	})

	t.Run("worktree-modified tracked file has worktree-side code", func(t *testing.T) {
		e, ok := byPath["base.go"]
		if !ok {
			t.Fatalf("base.go missing from %+v", entries)
		}
		if e.XY[1] != 'M' {
			t.Errorf("base.go XY = %q, want worktree-side 'M'", e.XY)
		}
	})

	t.Run("untracked file reported as ??", func(t *testing.T) {
		e, ok := byPath["untracked.txt"]
		if !ok {
			t.Fatalf("untracked.txt missing from %+v", entries)
		}
		if e.XY != "??" {
			t.Errorf("untracked.txt XY = %q, want %q", e.XY, "??")
		}
	})

	t.Run("clean subset: nothing unexpected", func(t *testing.T) {
		if len(entries) < 3 {
			t.Errorf("len(entries) = %d, want >= 3 (added, base, untracked)", len(entries))
		}
	})
}
