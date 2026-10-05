package issueflow

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
)

// newChainRepo creates a repository with one commit on main and returns its
// client and a helper that runs git in it. origin, when non-empty, is added as
// the "origin" remote.
func newChainRepo(t *testing.T, origin string) (*git.Client, func(args ...string)) {
	t.Helper()

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "T")
	run("config", "user.email", "t@t")
	run("config", "commit.gpgsign", "false")
	run("commit", "-q", "--allow-empty", "-m", "chore: init")
	if origin != "" {
		run("remote", "add", "origin", origin)
	}

	client, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return client, run
}

// newBareOrigin creates an empty bare repository and returns its path.
func newBareOrigin(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "origin.git")
	cmd := exec.CommandContext(t.Context(), "git", "init", "-q", "--bare", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	return dir
}

// writeLegacyBlob leaves at refs/zf/branches/<slug> the JSON blob an older
// git-zf wrote.
func writeLegacyBlob(t *testing.T, c *git.Client, slug, content string) {
	t.Helper()

	dir := c.WorkingTreeRoot()
	cmd := exec.CommandContext(t.Context(), "git", "-C", dir, "hash-object", "-w", "--stdin")
	cmd.Stdin = bytes.NewBufferString(content)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}

	sha := string(bytes.TrimSpace(out))
	if out, err := exec.CommandContext(t.Context(), "git", "-C", dir,
		"update-ref", "refs/zf/branches/"+slug, sha).CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %v\n%s", err, out)
	}
}
