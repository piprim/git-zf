package install

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/spf13/cobra"
)

type Install struct {
	appConfig *config.AppConfig
}

func New(appConfig *config.AppConfig) Install {
	return Install{appConfig: appConfig}
}

func (i Install) GetRootCmd() *cobra.Command {
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
				return fmt.Errorf("failed to install %s: %w", i.appConfig.ProgName, err)
			}

			fmt.Printf("Install %s to %s\n", i.appConfig.ProgName, path)

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
	if _, err := copyFile(dstFilePath, srcFilePath); err != nil {
		return "", err
	}

	return dstFilePath, nil
}

func copyFile(dstName, srcName string) (int64, error) {
	//nolint:gosec // srcName is given by os.Args[0]
	src, err := os.Open(srcName)
	if err != nil {
		return 0, fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(dstName, os.O_WRONLY|os.O_CREATE, 0755)
	if err != nil {
		return 0, fmt.Errorf("open destination: %w", err)
	}
	defer func() { _ = dst.Close() }()

	nb, err := io.Copy(dst, src)
	if err != nil {
		return 0, fmt.Errorf("faild to copy file: %w", err)
	}

	return nb, nil
}
