package config

import (
	"encoding/json"
	"fmt"

	appconfig "github.com/piprim/git-zf/config"
	"github.com/spf13/cobra"
)

func (c Config) getShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show active config file path and effective configuration",
		RunE:  c.showRunE,
	}
}

func (c Config) showRunE(cmd *cobra.Command, _ []string) error {
	path := c.appConfig.ConfigFile
	if path == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "Config file: no config file found (built-in defaults apply)")
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Config file: %s\n", path)
	}

	fmt.Fprintln(cmd.OutOrStdout())

	b, err := maskedJSON(c.appConfig)
	if err != nil {
		return err
	}

	fmt.Fprintln(cmd.OutOrStdout(), string(b))

	return nil
}

// maskedJSON renders cfg as indented JSON with the tracker token masked. cfg
// itself is left untouched.
func maskedJSON(cfg *appconfig.AppConfig) ([]byte, error) {
	out := *cfg
	if out.IssueTracker.Token != "" {
		out.IssueTracker.Token = "***"
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	return b, nil
}
