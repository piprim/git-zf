package branch

import (
	"bytes"
	"context"
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
			[]branchAction{actionCheckout, actionLog, actionDelete}},
		{"remote-only in progress", branch.Row{BranchName: "42@feat@x", Status: branch.StatusInProgress, RemoteOnly: true},
			[]branchAction{actionCheckout, actionAbandon, actionLog, actionDelete}},
		{"record without a branch", branch.Row{BranchName: "42@feat@x", Status: branch.StatusInProgress, NoBranch: true},
			[]branchAction{actionAbandon}},
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
			if got := menuEntriesFor(&tc.row); !slices.Equal(got, tc.want) {
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

func TestKeyActions(t *testing.T) {
	t.Run("every menu entry has one key", func(t *testing.T) {
		all := []branchAction{
			actionCheckout, actionIssueClose, actionMerge, actionPR,
			actionAbandon, actionTrack, actionLog, actionDelete,
		}
		if len(keyActions) != len(all) {
			t.Fatalf("len(keyActions) = %d, want %d", len(keyActions), len(all))
		}
		for _, a := range all {
			found := false
			for _, v := range keyActions {
				if v == a {
					found = true
				}
			}
			if !found {
				t.Errorf("missing key for %q", a)
			}
		}
	})
}

func TestOffersKey(t *testing.T) {
	allWired := &tableDeps{
		merge:      func(context.Context, SourceBranch) error { return nil },
		track:      func(context.Context, string) error { return nil },
		closeIssue: func(context.Context, string, string) error { return nil },
		log:        func(context.Context, string) error { return nil },
		pr:         func(context.Context, string) error { return nil },
	}
	for _, tc := range []struct {
		name string
		row  branch.Row
	}{
		{"remote-only", branch.Row{BranchName: "x", Plain: true, RemoteOnly: true}},
		{"plain", branch.Row{BranchName: "spike", Plain: true}},
		{"plain parseable", branch.Row{BranchName: "42@feat@untracked", Plain: true}},
		{"in progress", branch.Row{BranchName: "42@feat@x", Status: branch.StatusInProgress}},
		{"merged", branch.Row{BranchName: "42@feat@x", Status: branch.StatusMerged}},
		{"closed", branch.Row{BranchName: "42@feat@x", Status: branch.StatusClosed}},
		{"current plain", branch.Row{BranchName: "spike", Plain: true, Current: true}},
		{"current in progress", branch.Row{BranchName: "42@feat@x", Status: branch.StatusInProgress, Current: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offered := offeredEntries(allWired, &tc.row)
			for k, a := range keyActions {
				want := slices.Contains(offered, a)
				if got := offersKey(allWired, &tc.row, k); got != want {
					t.Errorf("offersKey(%q) = %v, want %v", k, got, want)
				}
			}
		})
	}

	t.Run("a nil func removes its key", func(t *testing.T) {
		d := *allWired // a copy: the other subtests keep every func
		d.merge = nil
		if offersKey(&d, &branch.Row{BranchName: "spike", Plain: true}, "m") {
			t.Error("m offered with nil merge")
		}
	})

	t.Run("an unknown key is not offered", func(t *testing.T) {
		if offersKey(allWired, &branch.Row{BranchName: "spike", Plain: true}, "?") {
			t.Error("? offered")
		}
	})
}
