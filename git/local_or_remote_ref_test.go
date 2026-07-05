package git

import "testing"

func TestLocalOrRemoteRef(t *testing.T) {
	t.Parallel()

	t.Run("local head exists → bare name", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newDiskRepoWithOrigin(t)
		if got := c.LocalOrRemoteRef("main"); got != "main" {
			t.Fatalf("LocalOrRemoteRef(main) = %q, want %q", got, "main")
		}
	})

	t.Run("local absent, remote configured → <remote>/<name>", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newDiskRepoWithOrigin(t)
		if got := c.LocalOrRemoteRef("feature-x"); got != "origin/feature-x" {
			t.Fatalf("LocalOrRemoteRef(feature-x) = %q, want %q", got, "origin/feature-x")
		}
	})

	t.Run("local absent, no remote → bare name", func(t *testing.T) {
		t.Parallel()
		c, _ := newDiskRepo(t)
		if got := c.LocalOrRemoteRef("feature-x"); got != "feature-x" {
			t.Fatalf("LocalOrRemoteRef(feature-x) = %q, want %q", got, "feature-x")
		}
	})
}
