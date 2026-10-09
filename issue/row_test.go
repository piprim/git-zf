package issue

import (
	"testing"
	"time"

	"github.com/piprim/git-zf/branch"
)

func TestCreatedCell(t *testing.T) {
	issueDate := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	branchDate := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	b := &branch.Row{CreatedAt: branchDate}

	for name, tc := range map[string]struct {
		row  Row
		want string
	}{
		"issue without a branch shows its own date":    {Row{CreatedAt: issueDate}, "2026-10-01"},
		"issue with a branch shows its own date":       {Row{CreatedAt: issueDate, Branch: b}, "2026-10-01"},
		"row without an issue date shows the branch's": {Row{Branch: b}, "2026-10-05"},
		"row with neither shows the empty marker":      {Row{}, "∅"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := CreatedCell(&tc.row); got != tc.want {
				t.Errorf("CreatedCell = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRowCells_ID(t *testing.T) {
	for name, tc := range map[string]struct {
		row  Row
		want string
	}{
		"a slug equal to the number stands alone":   {Row{IssueSlug: "11", TrackerID: "11"}, "11"},
		"a slug differing from the number shows it": {Row{IssueSlug: "edcac55", TrackerID: "11"}, "edcac55 (#11)"},
		"no number shows the slug":                  {Row{IssueSlug: "edcac55"}, "edcac55"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := RowCells(&tc.row, false)[0]; got != tc.want {
				t.Errorf("ID cell = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMatchesStatus_RepoIssueState(t *testing.T) {
	merged := &branch.Row{Status: branch.StatusMerged}

	for name, tc := range map[string]struct {
		row    Row
		status string
		want   bool
	}{
		"closed record without branch is not open":    {Row{State: "closed"}, StateOpen, false},
		"closed record without branch is closed":      {Row{State: "closed"}, StateClosed, true},
		"open record without branch is open":          {Row{State: "open"}, StateOpen, true},
		"open record with a merged branch stays open": {Row{State: "open", Branch: merged}, StateOpen, true},
		"open record with a merged branch not closed": {Row{State: "open", Branch: merged}, StateClosed, false},
		"any record matches all":                      {Row{State: "closed"}, "all", true},
		"row without state falls back to its branch":  {Row{Branch: merged}, StateClosed, true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.row.MatchesStatus(tc.status); got != tc.want {
				t.Errorf("MatchesStatus = %v, want %v", got, tc.want)
			}
		})
	}
}
