package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveGitFile(t *testing.T) {
	t.Parallel()

	t.Run("resolves relative target against repo root", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		gitFile := filepath.Join(root, ".git")
		target := "../../.git/modules/todo"

		if err := os.WriteFile(gitFile, []byte("gitdir: "+target+"\n"), 0o600); err != nil {
			t.Fatalf("write gitfile: %v", err)
		}

		got := resolveGitFile(gitFile, root)
		want := filepath.Clean(filepath.Join(root, target))

		if got != want {
			t.Errorf("resolveGitFile = %q, want %q", got, want)
		}
	})

	t.Run("preserves an absolute target path unchanged", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		gitFile := filepath.Join(root, ".git")
		abs := "/some/parent/.git/modules/todo"

		if err := os.WriteFile(gitFile, []byte("gitdir: "+abs+"\n"), 0o600); err != nil {
			t.Fatalf("write gitfile: %v", err)
		}

		got := resolveGitFile(gitFile, root)
		if got != abs {
			t.Errorf("resolveGitFile = %q, want %q", got, abs)
		}
	})

	t.Run("returns empty string when content lacks the gitdir prefix", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		gitFile := filepath.Join(root, ".git")

		if err := os.WriteFile(gitFile, []byte("not a gitfile\n"), 0o600); err != nil {
			t.Fatalf("write gitfile: %v", err)
		}

		if got := resolveGitFile(gitFile, root); got != "" {
			t.Errorf("resolveGitFile = %q, want empty string", got)
		}
	})

	t.Run("returns empty string when the file does not exist", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		if got := resolveGitFile(filepath.Join(root, ".git"), root); got != "" {
			t.Errorf("resolveGitFile = %q, want empty string", got)
		}
	})
}
