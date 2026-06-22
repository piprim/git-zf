package config

import (
	"encoding/json"
	"fmt"

	appconfig "github.com/piprim/git-zf/config"
	"github.com/spf13/cobra"
)

type issueTrackerOutput struct {
	Type  string `json:"type"`
	URL   string `json:"url"`
	Token string `json:"token"`
}

type configOutput struct {
	CommitTypes   []appconfig.CommitTypeOption  `json:"commit-types"`
	CommitMessage appconfig.CommitMessageConfig `json:"commit-message"`
	Branch        appconfig.BranchConfig        `json:"branch"`
	IssueTracker  issueTrackerOutput            `json:"issue-tracker"`
}

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

	out := toConfigOutput(c.appConfig)

	b, err := marshalConfig(&out)
	if err != nil {
		return err
	}

	fmt.Fprintln(cmd.OutOrStdout(), string(b))

	return nil
}

func toConfigOutput(cfg *appconfig.AppConfig) configOutput {
	token := cfg.IssueTracker.Token
	if token != "" {
		token = "***"
	}

	return configOutput{
		CommitTypes:   cfg.CommitTypes,
		CommitMessage: cfg.CommitMessage,
		Branch:        cfg.Branch,
		IssueTracker: issueTrackerOutput{
			Type:  cfg.IssueTracker.Type,
			URL:   cfg.IssueTracker.URL,
			Token: token,
		},
	}
}

func marshalConfig(out *configOutput) ([]byte, error) {
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	return b, nil
}
