package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/spf13/cobra"
)

// Cmd returns the `install` cobra command.
func Cmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install this tool to git-core as " + config.SubCommandName,
		RunE: func(cmd *cobra.Command, _ []string) error {
			appFilePath, err := exec.LookPath(os.Args[0])
			if err != nil {
				return fmt.Errorf(`failed to find executble "%s": %w`, os.Args[0], err)
			}

			path, err := installSubCmd(cmd.Context(), appFilePath)
			if err != nil {
				return fmt.Errorf("failed to install %s: %w", config.ProgName, err)
			}

			fmt.Printf("Install %s to %s\n", config.ProgName, path)

			return nil
		},
	}
}

func installSubCmd(ctx context.Context, srcFilePath string) (string, error) {
	dst, err := git.ExecPath(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve git-core path: %w", err)
	}

	dstFilePath := filepath.Join(dst, config.ProgName)
	//nolint:gosec // srcFilePath is given by os.Args[0]
	bin, err := os.ReadFile(srcFilePath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", srcFilePath, err)
	}

	//nolint:gosec // an installed command must be executable
	if err := os.WriteFile(dstFilePath, bin, 0o755); err != nil {
		return "", fmt.Errorf("write %s: %w", dstFilePath, err)
	}

	return dstFilePath, nil
}
