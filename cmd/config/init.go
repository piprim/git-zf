package config

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/charmbracelet/huh"
	toml "github.com/pelletier/go-toml"
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

func (c Config) initRunE(cmd *cobra.Command, _ []string) error {
	homePath, err := appconfig.HomePath()
	if err != nil {
		return fmt.Errorf("failed to load home config path: %w", err)
	}

	repoPath := appconfig.RepoPath()

	dest, err := pickDest(cmd.Context(), homePath, repoPath)
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

	if dest == homePath {
		return writeHomeDest(cmd, dest)
	}

	return writeRepoDest(cmd, dest, c.appConfig)
}

func pickDest(ctx context.Context, homePath, repoPath string) (string, error) {
	homeExists := fileExists(homePath)
	insideRepo := repoPath != ""

	if !insideRepo && !homeExists {
		return homePath, nil
	}

	repoExists := insideRepo && fileExists(repoPath)
	opts := buildPickerOpts(homePath, repoPath, homeExists, repoExists)

	var dest string
	if err := huh.NewForm(tui.ConfigDestPicker(opts, &dest)).RunWithContext(ctx); err != nil {
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

// writeHomeDest writes the full default TOML config to dest.
func writeHomeDest(cmd *cobra.Command, dest string) error {
	if err := os.WriteFile(dest, appconfig.DefaultTOML(), 0o600); err != nil {
		return fmt.Errorf("write config to %s: %w", dest, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Config written to %s\n", dest)

	return nil
}

// writeRepoDest shows a dynamic section picker populated from the current
// effective config, then writes only the selected sections as TOML to dest.
func writeRepoDest(cmd *cobra.Command, dest string, cfg *appconfig.AppConfig) error {
	sections, err := configToSectionMap(cfg)
	if err != nil {
		return fmt.Errorf("load config sections: %w", err)
	}

	keys := slices.Sorted(maps.Keys(sections))

	var selected []string
	if err := huh.NewForm(tui.ConfigSectionPicker(keys, &selected)).RunWithContext(cmd.Context()); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")

			return nil
		}

		return fmt.Errorf("section picker: %w", err)
	}

	if len(selected) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No sections selected. Nothing written.")

		return nil
	}

	filtered := make(map[string]any, len(selected))
	for _, k := range selected {
		filtered[k] = sections[k]
	}

	b, err := toml.Marshal(filtered)
	if err != nil {
		return fmt.Errorf("marshal repo config: %w", err)
	}

	if err := os.WriteFile(dest, b, 0o600); err != nil {
		return fmt.Errorf("write config to %s: %w", dest, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Config written to %s\n", dest)

	return nil
}

// configToSectionMap marshals cfg to TOML then unmarshals into a map so the
// section picker iterates dynamic keys derived from toml struct tags.
func configToSectionMap(cfg *appconfig.AppConfig) (map[string]any, error) {
	b, err := toml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config sections: %w", err)
	}

	m := make(map[string]any)
	if err := toml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("unmarshal config sections: %w", err)
	}

	return m, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}
