package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	appconfig "github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

func (c Config) getInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Write a default config file to home or repo directory",
		RunE:  c.initRunE,
	}
}

func (Config) initRunE(cmd *cobra.Command, _ []string) error {
	homePath, err := appconfig.HomePath()
	if err != nil {
		return fmt.Errorf("failed to load home config path: %w", err)
	}

	repoPath := appconfig.RepoPath()

	dest, err := pickDest(homePath, repoPath)
	if err != nil {
		return err
	}

	if dest == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")

		return nil
	}

	if fileExists(dest) {
		confirmed, err := confirmOverwrite(dest)
		if err != nil {
			return err
		}

		if !confirmed {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")

			return nil
		}
	}

	return writeDest(cmd, dest)
}

func pickDest(homePath, repoPath string) (string, error) {
	homeExists := fileExists(homePath)
	insideRepo := repoPath != ""

	if !insideRepo && !homeExists {
		return homePath, nil
	}

	repoExists := insideRepo && fileExists(repoPath)
	opts := buildPickerOpts(homePath, repoPath, homeExists, repoExists)

	var dest string
	if err := huh.NewForm(tui.ConfigDestPicker(opts, &dest)).Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", nil
		}

		return "", fmt.Errorf("destination picker: %w", err)
	}

	return dest, nil
}

func buildPickerOpts(homePath, repoPath string, homeExists, repoExists bool) []huh.Option[string] {
	homeLabel := fmt.Sprintf("Home (%s)", homePath)
	if homeExists {
		homeLabel += " [overwrite]"
	}

	opts := []huh.Option[string]{huh.NewOption(homeLabel, homePath)}

	if repoPath == "" {
		return opts
	}

	repoLabel := fmt.Sprintf("This repo (%s)", repoPath)
	if repoExists {
		repoLabel += " [overwrite]"
	} else {
		repoLabel += " [takes precedence over home]"
	}

	return append(opts, huh.NewOption(repoLabel, repoPath))
}

func confirmOverwrite(path string) (bool, error) {
	var confirmed bool
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("%s already exists. Overwrite?", path)).
			Value(&confirmed),
	)).Run()

	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, nil
		}

		return false, fmt.Errorf("confirm overwrite: %w", err)
	}

	return confirmed, nil
}

func writeDest(cmd *cobra.Command, dest string) error {
	if err := os.WriteFile(dest, appconfig.DefaultJSON(), 0o600); err != nil {
		return fmt.Errorf("write config to %s: %w", dest, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Config written to %s\n", dest)

	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}
