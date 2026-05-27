package branch

import (
	"context"
	"fmt"
	"testing"
)

// scriptedTrackerPrunePrompter returns a fixed map of decisions for tests.
type scriptedTrackerPrunePrompter struct {
	decisions map[string]string // branchName → "safe"|"force"|"skip"
	err       error             // set to simulate prompter failure
	calls     int               // observable for test assertions
}

func (p *scriptedTrackerPrunePrompter) DecideReap(_ context.Context, cands []trackerCandidate) (map[string]string, error) {
	p.calls++

	if p.err != nil {
		return nil, p.err
	}

	out := make(map[string]string, len(cands))
	for _, c := range cands {
		action, ok := p.decisions[c.BranchName]
		if !ok {
			action = "skip"
		}

		out[c.BranchName] = action
	}

	return out, nil
}

// Compile-time interface conformance.
var _ TrackerPrunePrompter = (*scriptedTrackerPrunePrompter)(nil)

func TestFixedActionPrompter(t *testing.T) {
	cases := []struct {
		name     string
		action   string
		cands    []trackerCandidate
		wantSize int
	}{
		{"safe-delete two", "safe", []trackerCandidate{{BranchName: "a"}, {BranchName: "b"}}, 2},
		{"force-delete one", "force", []trackerCandidate{{BranchName: "x"}}, 1},
		{"skip empty list returns empty map", "skip", nil, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newFixedActionPrompter(tc.action)
			got, err := p.DecideReap(context.Background(), tc.cands)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if len(got) != tc.wantSize {
				t.Fatalf("len = %d, want %d", len(got), tc.wantSize)
			}
			for _, c := range tc.cands {
				if got[c.BranchName] != tc.action {
					t.Fatalf("%s → %q, want %q", c.BranchName, got[c.BranchName], tc.action)
				}
			}
		})
	}
}

func TestScriptedPrompter_PropagatesError(t *testing.T) {
	t.Run("returns the configured error", func(t *testing.T) {
		wantErr := fmt.Errorf("simulated")
		p := &scriptedTrackerPrunePrompter{err: wantErr}
		if _, err := p.DecideReap(context.Background(), nil); err != wantErr {
			t.Fatalf("got %v, want %v", err, wantErr)
		}
	})
}
