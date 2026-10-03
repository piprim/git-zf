package issue

import (
	"slices"
	"strings"
	"testing"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/config"
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

// TestCloseRunE_InteractiveDispatch is a regression test for `git zf issue` →
// "Close": the menu dispatches through the issue root command, which defines no
// --base flag, and closeRunE failed with "read --base flag: flag accessed but
// not defined: base". The handler must treat a missing flag as "not set".
func TestCloseRunE_InteractiveDispatch(t *testing.T) {
	i := New(&config.AppConfig{})
	root := i.GetRootCmd()

	closeSub, _, err := root.Find([]string{"close"})
	if err != nil {
		t.Fatalf("find close subcommand: %v", err)
	}

	t.Run("issue root command defines no --base flag", func(t *testing.T) {
		if root.Flags().Lookup("base") != nil {
			t.Fatal("issue root command unexpectedly defines a --base flag")
		}
	})

	t.Run("close subcommand still defines --base", func(t *testing.T) {
		if closeSub.Flags().Lookup("base") == nil {
			t.Fatal("close subcommand lost its --base flag")
		}
	})

	t.Run("closeRunE on the root command does not read the undefined --base flag", func(t *testing.T) {
		t.Chdir(t.TempDir()) // a directory outside any git repo

		err := i.closeRunE(root, nil)
		if err == nil {
			t.Fatal("expected an error outside a git repo, got nil")
		}
		if strings.Contains(err.Error(), "flag accessed but not defined") {
			t.Fatalf("regression: dispatch path still reads the undefined --base flag: %v", err)
		}
	})

	t.Run("--base set on the close subcommand is still read", func(t *testing.T) {
		if err := closeSub.Flags().Set("base", "develop"); err != nil {
			t.Fatalf("set --base: %v", err)
		}
		if got := cmdutil.StringFlag(closeSub, "base"); got != "develop" {
			t.Errorf("StringFlag = %q, want %q", got, "develop")
		}
	})

	t.Run("a command without --base reads as empty", func(t *testing.T) {
		if got := cmdutil.StringFlag(root, "base"); got != "" {
			t.Errorf("StringFlag = %q, want empty", got)
		}
	})
}
