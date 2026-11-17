package mergeflow

import (
	"testing"

	"github.com/piprim/git-zf/git"
)

func TestHuhPrompter_CommitDefaults(t *testing.T) {
	rig := initRepo(t)
	p := &HuhPrompter{Client: rig.client}

	defaults := p.commitDefaults(t.Context())

	t.Run("asks for the merge options page only", func(t *testing.T) {
		if !defaults.MergeOnly {
			t.Error("MergeOnly = false: the merge commit form would show the full options page")
		}
	})

	t.Run("does not skip the options page", func(t *testing.T) {
		if defaults.Skip {
			t.Error("Skip = true, want the merge options page shown")
		}
	})

	t.Run("offers the repository's authors", func(t *testing.T) {
		if len(defaults.Authors) == 0 {
			t.Error("Authors is empty, want the repository's authors")
		}
	})

	t.Run("sets no commit flag", func(t *testing.T) {
		if defaults.CommitOptions != (git.CommitOptions{}) {
			t.Errorf("CommitOptions = %+v, want zero: a merge is committed as staged", defaults.CommitOptions)
		}
	})
}
