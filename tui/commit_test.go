package tui

import "testing"

func TestCommitOption_anyOptionSet(t *testing.T) {
	cases := []struct {
		name string
		opts CommitOption
		want bool
	}{
		{"zero value", CommitOption{}, false},
		{"authors slice alone is not a git flag", CommitOption{Authors: []string{"Alice"}}, false},
		{"All", CommitOption{All: true}, true},
		{"Amend", CommitOption{Amend: true}, true},
		{"NoVerify", CommitOption{NoVerify: true}, true},
		{"Signoff", CommitOption{Signoff: true}, true},
		{"AllowEmpty", CommitOption{AllowEmpty: true}, true},
		{"Author string", CommitOption{Author: "Alice <a@b.com>"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.opts.AnyOptionSet(); got != tc.want {
				t.Errorf("%+v: AnyOptionSet() = %v, want %v", tc.opts, got, tc.want)
			}
		})
	}
}
