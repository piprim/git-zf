package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveGitFile_relativeTarget(t *testing.T) {
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
}

func TestResolveGitFile_absoluteTarget(t *testing.T) {
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
}

func TestResolveGitFile_invalidContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	gitFile := filepath.Join(root, ".git")

	if err := os.WriteFile(gitFile, []byte("not a gitfile\n"), 0o600); err != nil {
		t.Fatalf("write gitfile: %v", err)
	}

	if got := resolveGitFile(gitFile, root); got != "" {
		t.Errorf("resolveGitFile = %q, want empty string", got)
	}
}

func TestResolveGitFile_missingFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if got := resolveGitFile(filepath.Join(root, ".git"), root); got != "" {
		t.Errorf("resolveGitFile = %q, want empty string", got)
	}
}
