package cmdutil

import (
	"fmt"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/spf13/cobra"
)

// NewClientForCmd builds a git.Client wired to cmd's IO streams and pins
// the remote from cfg when configured.
func NewClientForCmd(cmd *cobra.Command, cfg *config.AppConfig) (*git.Client, error) {
	c, err := git.NewClient(&pkg.IO{
		In:  cmd.InOrStdin(),
		Out: cmd.OutOrStdout(),
		Err: cmd.ErrOrStderr(),
	})
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	if cfg.Branch.Remote != "" {
		c.SetRemote(cfg.Branch.Remote)
	}
	return c, nil
}

// NewMainClientForCmd opens the repository like NewClientForCmd and re-anchors
// the client on the main working tree when the command was typed inside a
// linked worktree. invokedFrom is the working-tree root of the directory the
// command was typed in, so callers can tell whether a worktree they remove is
// the one the user's shell is standing in.
func NewMainClientForCmd(
	cmd *cobra.Command, cfg *config.AppConfig,
) (mainClient *git.Client, invokedFrom string, err error) {
	c, err := NewClientForCmd(cmd, cfg)
	if err != nil {
		return nil, "", err
	}
	invokedFrom, err = c.WorkingTreeRoot()
	if err != nil {
		return nil, "", fmt.Errorf("working tree root: %w", err)
	}
	mainClient, err = c.MainTree()
	if err != nil {
		return nil, "", fmt.Errorf("resolve main working tree: %w", err)
	}

	return mainClient, invokedFrom, nil
}

// StringFlag returns the value of the string flag name on cmd, or "" when cmd
// does not define it. Handlers reached from a menu run with the *parent*
// command (see RunMenu and the issue / branch menus), which lacks the
// subcommand's flags: a strict cmd.Flags().GetString then fails with "flag
// accessed but not defined". Reading through StringFlag treats a missing flag
// as "not set", which is what an interactive run means.
func StringFlag(cmd *cobra.Command, name string) string {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f.Value.String()
	}

	return ""
}
