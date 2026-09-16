package review

import (
	"slices"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/spf13/cobra"
)

func TestReviewRootCmd(t *testing.T) {
	t.Parallel()

	r := New(&config.AppConfig{})
	root := r.GetRootCmd()

	t.Run("runs a menu when invoked without a subcommand", func(t *testing.T) {
		t.Parallel()

		if root.RunE == nil {
			t.Fatal("review root command has no RunE; expected the action menu")
		}
	})

	t.Run("menu lists every user-facing subcommand in workflow order", func(t *testing.T) {
		t.Parallel()

		want := []string{"request", "start", "approve", "reject", "list", "status", "fetch", "sync", "track"}

		var got []string
		for _, sub := range r.menuSubs() {
			got = append(got, sub.Name())
		}

		if !slices.Equal(got, want) {
			t.Errorf("menu subs = %v, want %v", got, want)
		}
	})

	t.Run("menu excludes the hidden guard commands", func(t *testing.T) {
		t.Parallel()

		for _, sub := range r.menuSubs() {
			if sub.Hidden {
				t.Errorf("hidden subcommand %q is in the menu", sub.Name())
			}
		}
	})

	t.Run("hidden guard commands stay registered on the CLI", func(t *testing.T) {
		t.Parallel()

		for _, name := range []string{"guard", "guard-commit"} {
			if !slices.ContainsFunc(root.Commands(), func(c *cobra.Command) bool { return c.Name() == name }) {
				t.Errorf("subcommand %q not registered", name)
			}
		}
	})
}
