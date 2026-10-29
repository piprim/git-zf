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
		Long: `The configuration is a TOML file read from two places, the repository one
taking precedence over the home one:

  $HOME/.git-zf.toml            applied everywhere
  <repo>/.git/.git-zf.toml      inside .git/, never committed, may hold a
                                token

Every key is optional: the built-in defaults apply where a file says
nothing. init writes a default file, show prints the effective result.`,
	}
	cmd.AddCommand(c.getShowCmd(), c.getInitCmd())

	return cmd
}
