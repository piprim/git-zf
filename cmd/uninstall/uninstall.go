package uninstall

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/spf13/cobra"
)

// Cmd returns the `uninstall` cobra command.
func Cmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "uninstall " + config.SubCommandName + " from git-core",
		Long: `Remove the binary from git's exec path (git --exec-path), the one git zf
install put there. Run it with sudo when that directory is not writable by
you. The hooks installed by git zf init are left in place.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := uninstallSubCmd(cmd.Context())
			if err != nil {
				return fmt.Errorf("failed to uninstall %s: %w", config.ProgName, err)
			}

			fmt.Printf("uninstall %s from %s\n", config.ProgName, path)

			return nil
		},
	}
}

func uninstallSubCmd(ctx context.Context) (string, error) {
	dst, err := git.ExecPath(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve git-core path: %w", err)
	}

	dstFilePath := filepath.Join(dst, config.ProgName)
	if err := os.Remove(dstFilePath); err != nil {
		return "", fmt.Errorf("failed to remove %s from %s: %w", config.ProgName, dst, err)
	}

	return dst, nil
}
