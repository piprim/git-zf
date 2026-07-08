package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/piprim/git-zf/git"
)

func TestGroupEntries(t *testing.T) {
	t.Parallel()

	t.Run("staged-only entry lands in Changes to be committed", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: "M ", Path: "a.go"}}, false, false)

		if len(groups) != 1 || groups[0].Kind != kindStaged {
			t.Fatalf("groups = %+v, want single staged group", groups)
		}
		if groups[0].Lines[0].Word != "modified" || groups[0].Lines[0].Path != "a.go" {
			t.Errorf("line = %+v, want modified/a.go", groups[0].Lines[0])
		}
	})

	t.Run("unstaged-only entry lands in not-staged section", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: " M", Path: "b.go"}}, false, false)

		if len(groups) != 1 || groups[0].Kind != kindUnstaged {
			t.Fatalf("groups = %+v, want single unstaged group", groups)
		}
	})

	t.Run("MM entry appears in both staged and unstaged", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: "MM", Path: "c.go"}}, false, false)

		if len(groups) != 2 {
			t.Fatalf("len(groups) = %d, want 2", len(groups))
		}
		if groups[0].Kind != kindStaged || groups[1].Kind != kindUnstaged {
			t.Errorf("kinds = %v/%v, want staged/unstaged", groups[0].Kind, groups[1].Kind)
		}
		if groups[0].Lines[0].Path != "c.go" || groups[1].Lines[0].Path != "c.go" {
			t.Errorf("c.go should appear in both sections")
		}
	})

	t.Run("added and deleted words", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{
			{XY: "A ", Path: "new.go"},
			{XY: "D ", Path: "gone.go"},
		}, false, false)

		if len(groups) != 1 || groups[0].Kind != kindStaged {
			t.Fatalf("groups = %+v, want single staged group", groups)
		}
		if groups[0].Lines[0].Word != "new file" {
			t.Errorf("Word = %q, want %q", groups[0].Lines[0].Word, "new file")
		}
		if groups[0].Lines[1].Word != "deleted" {
			t.Errorf("Word = %q, want %q", groups[0].Lines[1].Word, "deleted")
		}
	})

	t.Run("renamed entry shows orig -> path in staged", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: "R ", Path: "new/name.go", OrigPath: "old/name.go"}}, false, false)

		if len(groups) != 1 || groups[0].Kind != kindStaged {
			t.Fatalf("groups = %+v, want single staged group", groups)
		}
		if groups[0].Lines[0].Word != "renamed" || groups[0].Lines[0].Path != "old/name.go -> new/name.go" {
			t.Errorf("line = %+v, want renamed/old->new", groups[0].Lines[0])
		}
	})

	t.Run("untracked entry lands in Untracked with bare path", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: "??", Path: "docs/notes.md"}}, false, false)

		if len(groups) != 1 || groups[0].Kind != kindUntracked {
			t.Fatalf("groups = %+v, want single untracked group", groups)
		}
		if groups[0].Lines[0].Word != "" || groups[0].Lines[0].Path != "docs/notes.md" {
			t.Errorf("line = %+v, want bare path", groups[0].Lines[0])
		}
	})

	t.Run("unmerged entry lands in Unmerged with descriptive word", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: "UU", Path: "conflict.go"}}, false, false)

		if len(groups) != 1 || groups[0].Kind != kindUnmerged {
			t.Fatalf("groups = %+v, want single unmerged group", groups)
		}
		if groups[0].Lines[0].Word != "both modified" {
			t.Errorf("Word = %q, want %q", groups[0].Lines[0].Word, "both modified")
		}
	})

	t.Run("double-letter conflict codes route to Unmerged not staged/unstaged", func(t *testing.T) {
		t.Parallel()

		for _, xy := range []string{"AA", "DD"} {
			xy := xy
			t.Run(xy, func(t *testing.T) {
				t.Parallel()

				groups := groupEntries([]git.StatusEntry{{XY: xy, Path: "c.go"}}, false, false)
				if len(groups) != 1 || groups[0].Kind != kindUnmerged {
					t.Fatalf("XY %q: groups = %+v, want single unmerged group", xy, groups)
				}
				if groups[0].Lines[0].Word != unmergedWords[xy] {
					t.Errorf("XY %q: Word = %q, want %q", xy, groups[0].Lines[0].Word, unmergedWords[xy])
				}
			})
		}
	})

	t.Run("empty input yields no groups", func(t *testing.T) {
		t.Parallel()

		if groups := groupEntries(nil, false, false); len(groups) != 0 {
			t.Errorf("len(groups) = %d, want 0", len(groups))
		}
	})

	t.Run("section order is staged, unstaged, untracked, unmerged", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{
			{XY: "??", Path: "u.txt"},
			{XY: "UU", Path: "c.go"},
			{XY: " M", Path: "b.go"},
			{XY: "M ", Path: "a.go"},
		}, false, false)

		want := []statusKind{kindStaged, kindUnstaged, kindUntracked, kindUnmerged}
		if len(groups) != len(want) {
			t.Fatalf("len(groups) = %d, want %d", len(groups), len(want))
		}
		for i := range want {
			if groups[i].Kind != want[i] {
				t.Errorf("groups[%d].Kind = %v, want %v", i, groups[i].Kind, want[i])
			}
		}
	})

	t.Run("all=true folds unstaged tracked change into to-be-committed", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{
			{XY: " M", Path: "plop"},     // tracked, worktree-modified only
			{XY: "??", Path: "notes.md"}, // untracked
		}, true, false)

		if len(groups) != 2 {
			t.Fatalf("len(groups) = %d, want 2 (staged + untracked, no unstaged)", len(groups))
		}
		if groups[0].Kind != kindStaged || groups[1].Kind != kindUntracked {
			t.Fatalf("kinds = %v/%v, want staged/untracked", groups[0].Kind, groups[1].Kind)
		}
		if groups[0].Lines[0].Word != "modified" || groups[0].Lines[0].Path != "plop" {
			t.Errorf("staged line = %+v, want modified/plop", groups[0].Lines[0])
		}
	})

	t.Run("all=true never emits an unstaged section", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: " M", Path: "a.go"}}, true, false)

		for _, g := range groups {
			if g.Kind == kindUnstaged {
				t.Errorf("groups contain a kindUnstaged section under all=true: %+v", groups)
			}
		}
	})

	t.Run("all=true dedups a path staged and worktree-modified (MM) to one staged line", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: "MM", Path: "c.go"}}, true, false)

		if len(groups) != 1 || groups[0].Kind != kindStaged {
			t.Fatalf("groups = %+v, want single staged group", groups)
		}
		if len(groups[0].Lines) != 1 || groups[0].Lines[0].Path != "c.go" {
			t.Errorf("staged lines = %+v, want exactly one c.go line", groups[0].Lines)
		}
	})

	t.Run("includeUntracked folds untracked into to-be-committed as new file", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{
			{XY: "M ", Path: "a.go"},
			{XY: "??", Path: "notes.md"},
		}, false, true)

		if len(groups) != 1 || groups[0].Kind != kindStaged {
			t.Fatalf("groups = %+v, want single staged group (no untracked section)", groups)
		}
		var found bool
		for _, ln := range groups[0].Lines {
			if ln.Path == "notes.md" {
				found = true
				if ln.Word != "new file" {
					t.Errorf("notes.md Word = %q, want %q", ln.Word, "new file")
				}
			}
		}
		if !found {
			t.Errorf("notes.md not folded into staged section: %+v", groups[0].Lines)
		}
	})

	t.Run("includeUntracked composes with all=true", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{
			{XY: " M", Path: "plop"},     // tracked, worktree-modified only
			{XY: "??", Path: "notes.md"}, // untracked
		}, true, true)

		if len(groups) != 1 || groups[0].Kind != kindStaged {
			t.Fatalf("groups = %+v, want single staged group", groups)
		}
		for _, g := range groups {
			if g.Kind == kindUnstaged || g.Kind == kindUntracked {
				t.Errorf("unexpected %v section under all=true, includeUntracked=true: %+v", g.Kind, groups)
			}
		}
		// Both folds land their line in the single staged section: "plop" from
		// the --all fold (as "modified") and "notes.md" from the untracked fold
		// (as "new file").
		byPath := make(map[string]string, len(groups[0].Lines))
		for _, ln := range groups[0].Lines {
			byPath[ln.Path] = ln.Word
		}
		if byPath["plop"] != "modified" {
			t.Errorf("staged 'plop' Word = %q, want %q", byPath["plop"], "modified")
		}
		if byPath["notes.md"] != "new file" {
			t.Errorf("staged 'notes.md' Word = %q, want %q", byPath["notes.md"], "new file")
		}
	})

	t.Run("includeUntracked=false leaves untracked in its own section", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: "??", Path: "notes.md"}}, false, false)

		if len(groups) != 1 || groups[0].Kind != kindUntracked {
			t.Fatalf("groups = %+v, want single untracked group (unchanged default behavior)", groups)
		}
	})
}

func TestStatusPanel(t *testing.T) {
	t.Parallel()

	t.Run("no entries returns empty string", func(t *testing.T) {
		t.Parallel()

		if got := StatusPanel(nil, false, false); got != "" {
			t.Errorf("StatusPanel(nil) = %q, want \"\"", got)
		}
	})

	t.Run("renders section headers and word: path lines", func(t *testing.T) {
		t.Parallel()

		out := StatusPanel([]git.StatusEntry{
			{XY: "A ", Path: "git/status.go"},
			{XY: " M", Path: "commit/form.go"},
			{XY: "??", Path: "docs/notes.md"},
		}, false, false)

		for _, want := range []string{
			"Current Git Status",
			"Changes to be committed:",
			"new file: git/status.go",
			"Changes not staged for commit:",
			"modified: commit/form.go",
			"Untracked files:",
			"docs/notes.md",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("panel missing %q\n---\n%s", want, out)
			}
		}
	})

	t.Run("rename entry shows orig -> path", func(t *testing.T) {
		t.Parallel()

		out := StatusPanel([]git.StatusEntry{{XY: "R ", Path: "new/name.go", OrigPath: "old/name.go"}}, false, false)

		if !strings.Contains(out, "renamed: old/name.go -> new/name.go") {
			t.Errorf("panel missing rename line\n---\n%s", out)
		}
	})

	t.Run("all=true shows tracked changes under to-be-committed, not 'not staged'", func(t *testing.T) {
		t.Parallel()

		out := StatusPanel([]git.StatusEntry{
			{XY: " M", Path: "plop"},
			{XY: "??", Path: "notes.md"},
		}, true, false)

		if !strings.Contains(out, "Changes to be committed:") || !strings.Contains(out, "modified: plop") {
			t.Errorf("panel should list 'modified: plop' under 'Changes to be committed:'\n---\n%s", out)
		}
		if strings.Contains(out, "Changes not staged for commit:") {
			t.Errorf("panel must NOT contain a 'not staged' section under -a\n---\n%s", out)
		}
		if !strings.Contains(out, "Untracked files:") || !strings.Contains(out, "notes.md") {
			t.Errorf("untracked files must still be shown separately\n---\n%s", out)
		}
	})

	t.Run("includeUntracked shows untracked under to-be-committed and no Untracked section", func(t *testing.T) {
		t.Parallel()

		out := StatusPanel([]git.StatusEntry{
			{XY: "A ", Path: "git/status.go"},
			{XY: "??", Path: "docs/notes.md"},
		}, false, true)

		if !strings.Contains(out, "Changes to be committed:") || !strings.Contains(out, "new file: docs/notes.md") {
			t.Errorf("panel should show 'new file: docs/notes.md' under 'Changes to be committed:'\n---\n%s", out)
		}
		if strings.Contains(out, "Untracked files:") {
			t.Errorf("panel must NOT contain an 'Untracked files:' section under --include-untracked\n---\n%s", out)
		}
	})

	t.Run("StatusPanelReserveWidth covers all four all/includeUntracked combos", func(t *testing.T) {
		t.Parallel()

		entries := []git.StatusEntry{
			{XY: " M", Path: "some/longish/path/to/plop.go"},
			{XY: "??", Path: "another/untracked/file/notes.md"},
		}
		w := StatusPanelReserveWidth(entries)

		for _, all := range []bool{false, true} {
			for _, iu := range []bool{false, true} {
				if pw := lipgloss.Width(StatusPanel(entries, all, iu)); pw > w {
					t.Errorf("ReserveWidth = %d, but StatusPanel(all=%v, iu=%v) width = %d (must be >= all combos)", w, all, iu, pw)
				}
			}
		}
	})
}
