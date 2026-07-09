package branch

import "testing"

func TestReviewBranchName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		issueSlug string
		want      string
	}{
		{"42", "42@review"},
		{"PROJ-7", "PROJ-7@review"},
	}
	for _, tt := range tests {
		t.Run(tt.issueSlug, func(t *testing.T) {
			t.Parallel()

			if got := ReviewBranchName(tt.issueSlug); got != tt.want {
				t.Errorf("ReviewBranchName(%q) = %q, want %q", tt.issueSlug, got, tt.want)
			}
		})
	}
}

func TestIsReviewBranch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		label string
		name  string
		want  bool
	}{
		{"review branch", "42@review", true},
		{"feature branch", "42@feat@add-login", false},
		{"bare review word", "review", false},
		{"empty name", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			t.Parallel()

			if got := IsReviewBranch(tt.name); got != tt.want {
				t.Errorf("IsReviewBranch(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestCutReviewSuffix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		label    string
		name     string
		wantSlug string
		wantOK   bool
	}{
		{"review branch", "42@review", "42", true},
		{"feature branch", "42@feat@add-login", "42@feat@add-login", false},
		{"empty name", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			t.Parallel()

			slug, ok := CutReviewSuffix(tt.name)
			if slug != tt.wantSlug || ok != tt.wantOK {
				t.Errorf("CutReviewSuffix(%q) = (%q, %v), want (%q, %v)", tt.name, slug, ok, tt.wantSlug, tt.wantOK)
			}
		})
	}
}
