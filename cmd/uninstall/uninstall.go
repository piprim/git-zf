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

type Uninstall struct {
	appConfig *config.AppConfig
}

func New(appConfig *config.AppConfig) Uninstall {
	return Uninstall{appConfig: appConfig}
}

func (u Uninstall) GetRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "uninstall " + config.SubCommandName + " from git-core",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := uninstallSubCmd(cmd.Context())
			if err != nil {
				return fmt.Errorf("failed to uninstall %s: %w", u.appConfig.ProgName, err)
			}

			fmt.Printf("uninstall %s from %s\n", u.appConfig.ProgName, path)

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
