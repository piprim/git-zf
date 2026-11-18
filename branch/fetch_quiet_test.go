package branch

import (
	"bytes"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
)

func TestFetchQuiet(t *testing.T) {
	t.Parallel()

	t.Run("fetches like Fetch", func(t *testing.T) {
		t.Parallel()

		origin := newOrigin(t)
		alice, bob := newRepo(t, "alice", origin), newRepo(t, "bob", origin)
		ctx := t.Context()

		mustStart(t, alice, "42", Op{Branch: "42@feat@login", BranchType: "feat", Title: "Add login"})
		if err := Push(ctx, alice, "42"); err != nil {
			t.Fatalf("Push: %v", err)
		}
		if err := FetchQuiet(ctx, bob); err != nil {
			t.Fatalf("FetchQuiet: %v", err)
		}
		if got := mustLoad(t, bob, "42"); got.Title != "Add login" {
			t.Errorf("Load = %+v", got)
		}
	})

	t.Run("prints nothing when the remote is down", func(t *testing.T) {
		t.Parallel()

		dir := newRepo(t, "alice", "/nonexistent/origin.git").WorkingTreeRoot()
		var out, errOut bytes.Buffer
		c, err := git.NewClientAt(&pkg.IO{Out: &out, Err: &errOut}, dir)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}

		err = FetchQuiet(t.Context(), c)
		if err == nil {
			t.Error("FetchQuiet returned no error for a dead remote")
		}
		if out.Len()+errOut.Len() != 0 {
			t.Errorf("printed %q / %q, want nothing", out.String(), errOut.String())
		}
	})
}
