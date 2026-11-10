package branch

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
)

func TestMenuEntriesFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  branch.Row
		want []branchAction
	}{
		{"remote-only", branch.Row{BranchName: "x", Plain: true, RemoteOnly: true},
			[]branchAction{actionCheckout, actionLog}},
		{"plain", branch.Row{BranchName: "spike", Plain: true},
			[]branchAction{actionCheckout, actionTrack, actionMerge, actionPR, actionLog, actionDelete}},
		{"plain row with a parseable name omits merge", branch.Row{BranchName: "42@feat@untracked", Plain: true},
			[]branchAction{actionCheckout, actionTrack, actionPR, actionLog, actionDelete}},
		{"in progress", branch.Row{BranchName: "42@feat@x", Status: branch.StatusInProgress},
			[]branchAction{actionCheckout, actionIssueClose, actionPR, actionAbandon, actionLog, actionDelete}},
		{"merged", branch.Row{BranchName: "42@feat@x", Status: branch.StatusMerged},
			[]branchAction{actionCheckout, actionLog, actionDelete}},
		{"closed", branch.Row{BranchName: "42@feat@x", Status: branch.StatusClosed},
			[]branchAction{actionCheckout, actionTrack, actionLog, actionDelete}},
		{"current plain", branch.Row{BranchName: "spike", Plain: true, Current: true},
			[]branchAction{actionTrack, actionPR, actionLog}},
		{"current in progress", branch.Row{BranchName: "42@feat@x", Status: branch.StatusInProgress, Current: true},
			[]branchAction{actionIssueClose, actionPR, actionAbandon, actionLog}},
		{"current merged", branch.Row{BranchName: "42@feat@x", Status: branch.StatusMerged, Current: true},
			[]branchAction{actionLog}},
		{"current closed", branch.Row{BranchName: "42@feat@x", Status: branch.StatusClosed, Current: true},
			[]branchAction{actionTrack, actionLog}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := menuEntriesFor(tc.row); !slices.Equal(got, tc.want) {
				t.Errorf("menuEntriesFor = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAfterAction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reopen bool
		err    error
		want   bool
		stderr string
	}{
		{"an aborted form reopens quietly", false, fmt.Errorf("issue form: %w", huh.ErrUserAborted), true, ""},
		{"an error is printed and reopens", false, errors.New("boom"), true, "error: boom\n"},
		{"checkout does not reopen", false, nil, false, ""},
		{"delete reopens", true, nil, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if got := afterAction(tc.reopen, tc.err, &buf); got != tc.want || buf.String() != tc.stderr {
				t.Errorf("afterAction = %v, stderr = %q", got, buf.String())
			}
		})
	}
}
