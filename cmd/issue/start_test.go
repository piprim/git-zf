package issue

import (
	"strings"
	"testing"

	"github.com/piprim/git-zf/config"
)

// TestStartRunE_InteractiveDispatch is a regression test for the bug where
// `git zf issue` → "Start" dispatched through the issue root command (which
// defines no --variant flag) and startRunE tried to read that flag, failing
// with "read --variant flag: flag accessed but not defined: variant". startRunE
// must take the variant as a parameter so the interactive dispatcher passes "".
func TestStartRunE_InteractiveDispatch(t *testing.T) {
	i := New(&config.AppConfig{})
	root := i.GetRootCmd()

	t.Run("issue root command defines no --variant flag", func(t *testing.T) {
		if root.Flags().Lookup("variant") != nil {
			t.Fatal("issue root command unexpectedly defines a --variant flag")
		}
	})

	t.Run("start subcommand still defines --variant", func(t *testing.T) {
		startSub, _, err := root.Find([]string{"start"})
		if err != nil {
			t.Fatalf("find start subcommand: %v", err)
		}
		if startSub.Flags().Lookup("variant") == nil {
			t.Fatal("start subcommand lost its --variant flag")
		}
	})

	t.Run("startRunE on the root command does not read the undefined --variant flag", func(t *testing.T) {
		t.Chdir(t.TempDir()) // a directory outside any git repo

		err := i.startRunE(root, "", "")
		if err == nil {
			t.Fatal("expected an error outside a git repo, got nil")
		}
		if strings.Contains(err.Error(), "flag accessed but not defined") {
			t.Fatalf("regression: dispatch path still reads the undefined --variant flag: %v", err)
		}
		// The flow fails later, at repo detection — proving flag reading was
		// passed without error.
		if !strings.Contains(err.Error(), "git repository") {
			t.Fatalf("expected a git-repository error, got: %v", err)
		}
	})
}
