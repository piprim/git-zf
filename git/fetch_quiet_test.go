package git

import (
	"bytes"
	"testing"

	"github.com/piprim/git-zf/internal/pkg"
)

func TestFetchQuiet(t *testing.T) {
	t.Parallel()

	t.Run("fetches like Fetch", func(t *testing.T) {
		t.Parallel()

		alice, aliceDir, originDir := newDiskRepoWithOrigin(t)
		_, bobDir := cloneOf(t, originDir, "bob")
		writeFile(t, bobDir, "bob.go", "package main\n")
		runGitInDir(t, bobDir, "add", "bob.go")
		runGitInDir(t, bobDir, "commit", "-m", "bob")
		runGitInDir(t, bobDir, "push", "-q", "origin", "main")

		if err := alice.FetchQuiet(t.Context()); err != nil {
			t.Fatalf("FetchQuiet: %v", err)
		}
		runGitInDir(t, aliceDir, "merge-base", "--is-ancestor", "HEAD", "origin/main")
		bs, _, err := alice.Status(t.Context())
		if err != nil || bs.Behind != 1 {
			t.Errorf("status = %+v, %v, want one commit behind", bs, err)
		}
	})

	t.Run("prints nothing when the remote is down", func(t *testing.T) {
		t.Parallel()

		_, dir, _ := newDiskRepoWithOrigin(t)
		runGitInDir(t, dir, "remote", "set-url", "origin", "/nonexistent/origin.git")
		var out, errOut bytes.Buffer
		c, err := NewClientAt(&pkg.IO{Out: &out, Err: &errOut}, dir)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}

		if err := c.FetchQuiet(t.Context()); err == nil {
			t.Error("FetchQuiet returned no error for a dead remote")
		}
		if out.Len()+errOut.Len() != 0 {
			t.Errorf("printed %q / %q, want nothing", out.String(), errOut.String())
		}
	})
}
