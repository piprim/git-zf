package issue

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
)

// writeLegacyBranchBlob leaves at refs/zf/branches/<slug> the JSON blob an
// older git-zf wrote.
func writeLegacyBranchBlob(t *testing.T, dir, slug, content string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "-C", dir, "hash-object", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(content)
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

// A sub-task whose parent is still tracked in the old blob format must not be
// closed: the parent's branch is unknown, and falling back to the base branch
// would merge the sub-task into main.
func TestClose_RefusesWhenTheParentRefIsALegacyBlob(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	branchtest.Amend(t, rig.client, branch.Op{Branch: "ABC-1@feat@add-thing", Parent: "X"})
	writeLegacyBranchBlob(t, rig.dir, "X", `{"issue_slug":"X","branch_name":"X@feat@big"}`)

	p := &scriptedPrompter{Branch: rig.pickedBranchRow()}
	err := runClose(t.Context(), rig.deps(), p)

	t.Run("the close is refused with ErrLegacyBranch", func(t *testing.T) {
		if !errors.Is(err, branch.ErrLegacyBranch) {
			t.Fatalf("err = %v, want ErrLegacyBranch", err)
		}
	})

	t.Run("the error names the parent and the way out", func(t *testing.T) {
		for _, want := range []string{`"X"`, "git zf issue track"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error lacks %q: %v", want, err)
			}
		}
	})

	t.Run("main is unchanged", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "chore: init")
	})

	t.Run("the branch is still in progress", func(t *testing.T) {
		_, e, findErr := branch.Find(t.Context(), rig.client, "ABC-1@feat@add-thing")
		if findErr != nil || e == nil || e.Status != branch.StatusInProgress {
			t.Errorf("entry = %+v, %v", e, findErr)
		}
	})
}

// A parent is closed only once every branch of every sub-task is merged.
func TestClose_ParentBlockedByAnOpenSubTask(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	branchtest.Seed(t, rig.client, branch.Op{Branch: "ABC-1.1@feat@part", Parent: "ABC-1"}, branch.StatusMerged)
	branchtest.Seed(t, rig.client, branch.Op{Branch: "ABC-1.2@feat@other", Parent: "ABC-1"}, branch.StatusClosed)

	p := &scriptedPrompter{Branch: rig.pickedBranchRow()}
	err := runClose(t.Context(), rig.deps(), p)

	t.Run("the close names the sub-task that is not merged", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "open sub-tasks: [ABC-1.2]") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("main is unchanged", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "chore: init")
	})
}
