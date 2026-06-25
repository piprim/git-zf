package commit

import (
	"testing"

	"github.com/piprim/git-zf/cmd/pushflow"
	"github.com/spf13/cobra"
)

// TestCommitPushDecision exercises the --push/--no-push/-y flag → (skip,
// autoConfirm) decision in isolation from the store/repo.
func TestCommitPushDecision(t *testing.T) {
	t.Parallel()

	newCmd := func(args ...string) *cobra.Command {
		cmd := &cobra.Command{Use: "commit", RunE: func(*cobra.Command, []string) error { return nil }}
		cmd.Flags().BoolP("yes", "y", false, "")
		pushflow.AddFlags(cmd)
		_ = cmd.ParseFlags(args)
		return cmd
	}

	t.Run("--no-push → skip", func(t *testing.T) {
		t.Parallel()
		cmd := newCmd("--no-push")
		push, noPush := pushflow.ReadFlags(cmd)
		skip, _, err := pushflow.ResolveFlags(push, noPush, true)
		if err != nil || !skip {
			t.Fatalf("got skip=%v err=%v, want skip=true", skip, err)
		}
	})

	t.Run("--push → auto-confirm", func(t *testing.T) {
		t.Parallel()
		cmd := newCmd("--push")
		push, noPush := pushflow.ReadFlags(cmd)
		skip, auto, err := pushflow.ResolveFlags(push, noPush, true)
		if err != nil || skip || !auto {
			t.Fatalf("got skip=%v auto=%v err=%v, want skip=false auto=true", skip, auto, err)
		}
	})

	t.Run("-y maps to NonInteractive", func(t *testing.T) {
		t.Parallel()
		cmd := newCmd("-y")
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			t.Fatal("yes flag not parsed")
		}
	})
}
