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
