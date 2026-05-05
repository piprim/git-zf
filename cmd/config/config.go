package config

import (
	appconfig "github.com/piprim/git-zf/config"
	"github.com/spf13/cobra"
)

// Config holds shared state for the config command group.
type Config struct {
	appConfig *appconfig.AppConfig
}

// New creates a Config command handler.
func New(appConfig *appconfig.AppConfig) Config {
	return Config{appConfig: appConfig}
}

// GetRootCmd returns the root cobra command for the config group.
func (c Config) GetRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage git-zf configuration",
	}
	cmd.AddCommand(c.getShowCmd(), c.getInitCmd())

	return cmd
}
