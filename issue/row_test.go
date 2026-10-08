package issue

import (
	"testing"

	"github.com/piprim/git-zf/branch"
)

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
