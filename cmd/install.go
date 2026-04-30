package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	subCommandName = "zf"
)

// InstallCmd copies the current binary into Git's exec path as "git-zf".
func getInstallCmd() *cobra.Command {
	var installCmd = &cobra.Command{
		Use:   "install",
		Short: "Install this tool to git-core as " + progName,
		RunE: func(cmd *cobra.Command, _ []string) error {
			appFilePath, err := exec.LookPath(os.Args[0])
			if err != nil {
				return fmt.Errorf(`failed to find executble "%s": %w`, os.Args[0], err)
			}

			path, err := installSubCmd(cmd.Context(), appFilePath)
			if err != nil {
				return fmt.Errorf("failed to install %s: %w", Name, err)
			}

			fmt.Printf("Install %s to %s\n", progName, path)

			return nil
		},
	}

	return installCmd
}

// installSubCmd copies srcFilePath into Git's exec path as "git-<subCmdName>".
func installSubCmd(ctx context.Context, srcFilePath string) (string, error) {
	dst, err := gitExecPath(ctx)
	if err != nil {
		return "", err
	}

	dstFilePath := filepath.Join(dst, "git-"+subCommandName)
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

func gitExecPath(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "--exec-path")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("exec-path pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("exec-path start: %w", err)
	}
	result, err := io.ReadAll(stdout)
	if err != nil {
		return "", fmt.Errorf("exec-path read: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("exec-path wait: %w", err)
	}

	return strings.TrimSpace(string(result)), nil
}
