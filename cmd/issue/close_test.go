package issue

import (
	"slices"
	"testing"
)

// TestMergeTargetCandidates pins the merge-target candidate rules: defaultBase
// leads the list (even when remote-only), the branch being closed and @review
// branches are never offered, duplicates and empty names are dropped.
func TestMergeTargetCandidates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		locals      []string
		closing     string
		defaultBase string
		want        []string
	}{
		{
			name:        "default base placed first and deduped against locals",
			locals:      []string{"other", "main", "second"},
			closing:     "ABC-1@feat@x",
			defaultBase: "main",
			want:        []string{"main", "other", "second"},
		},
		{
			name:        "closing branch excluded",
			locals:      []string{"main", "ABC-1@feat@x"},
			closing:     "ABC-1@feat@x",
			defaultBase: "main",
			want:        []string{"main"},
		},
		{
			name:        "review branches excluded",
			locals:      []string{"main", "ABC-1@review", "other"},
			closing:     "ABC-1@feat@x",
			defaultBase: "main",
			want:        []string{"main", "other"},
		},
		{
			name:        "remote-only default base included when absent from locals",
			locals:      []string{"X.2@feat@part"},
			closing:     "X.1@feat@other",
			defaultBase: "X@feat@big",
			want:        []string{"X@feat@big", "X.2@feat@part"},
		},
		{
			name:        "empty names skipped",
			locals:      []string{"", "main"},
			closing:     "ABC-1@feat@x",
			defaultBase: "main",
			want:        []string{"main"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := mergeTargetCandidates(tc.locals, tc.closing, tc.defaultBase)
			if !slices.Equal(got, tc.want) {
				t.Errorf("mergeTargetCandidates(%v, %q, %q) = %v, want %v",
					tc.locals, tc.closing, tc.defaultBase, got, tc.want)
			}
		})
	}
}
