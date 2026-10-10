package completion

import (
	"errors"
	"fmt"
	"io"

	"github.com/piprim/git-zf/config"
	"github.com/spf13/cobra"
)

// gitBridge completes `git zf …`. Git's completion calls _git_zf for the
// subcommand; the function rewrites the command line to `git-zf …` and hands
// it to cobra's function. Only the words are rewritten: the line is rebuilt
// from them, so a cursor in the middle of the line completes as if at its end.
const gitBridge = `
# Completion of "git zf …" through git's own completion, which calls _git_zf.
_git_zf()
{
    local i=1
    while [[ $i -lt ${#COMP_WORDS[@]} && ${COMP_WORDS[i]} != zf ]]; do ((i++)); done
    [[ $i -lt ${#COMP_WORDS[@]} ]] || i=1 # an alias of zf: assume "git <alias> …"
    local COMP_CWORD=$((COMP_CWORD - i))
    local COMP_WORDS=("git-zf" "${COMP_WORDS[@]:i+1}")
    local COMP_LINE="${COMP_WORDS[*]}"
    local COMP_POINT=${#COMP_LINE}
    __start_git-zf
}
`

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
			out := cmd.OutOrStdout()
			switch shell {
			case "bash":
				if err = cmd.Root().GenBashCompletion(out); err == nil {
					_, err = io.WriteString(out, gitBridge)
				}
			case "zsh":
				err = cmd.Root().GenZshCompletion(out)
			case "fish":
				err = cmd.Root().GenFishCompletion(out, true)
			case "powershell":
				err = cmd.Root().GenPowerShellCompletionWithDesc(out)
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
