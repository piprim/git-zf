package completion

import (
	"errors"
	"fmt"
	"os"

	"github.com/piprim/git-zf/config"
	"github.com/spf13/cobra"
)

// Cmd returns the `completion` cobra command.
func Cmd() *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate completion script",
		Long: `To load completions:
Bash: $ source <(yourprogram completion bash)
Zsh: $ source <(yourprogram completion zsh)
Fish: $ yourprogram completion fish | source
PowerShell: PS> yourprogram completion powershell | Out-String | Invoke-Expression
`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.ExactValidArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			shell := args[0]
			switch shell {
			case "bash":
				err = cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				err = cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				err = cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				err = cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				err = errors.New("shell not supported")
			}

			if err != nil {
				return fmt.Errorf("%s failed to generate %s completion: %w", config.ProgName, shell, err)
			}

			return nil
		},
	}
}
