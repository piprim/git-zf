package issue

import (
	"context"
	"fmt"
	"strings"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/spf13/cobra"
)

func (i Issue) getLabelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "label [<id> [+add|-remove]...]",
		Short: "Add or remove labels on an issue stored in the repository",
		Long: `Add (+name) or remove (-name) labels on an issue stored in the repository
and push it:

    git zf issue label 1a2b3c4 +bug -wontfix

<id> is the full ID or a unique prefix of at least 4 characters. Without
arguments a picker lists the issues, then a form asks for the changes.`,
	}

	// Everything after <id> is positional, so "-wontfix" is not read as flags.
	cmd.Flags().SetInterspersed(false)

	cmd.RunE = i.labelRunE

	return cmd
}

func (i Issue) labelRunE(cmd *cobra.Command, args []string) error {
	client, err := cmdutil.NewClientForCmd(cmd, i.appConfig)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}

	return runLabel(cmd.Context(), client, huhRecordPrompter{}, args)
}

// parseLabelChanges turns "+add" / "-remove" tokens into ops.
func parseLabelChanges(tokens []string) ([]issuepkg.Op, error) {
	ops := make([]issuepkg.Op, 0, len(tokens))
	for _, tok := range tokens {
		name := strings.TrimSpace(tok[min(1, len(tok)):])

		switch {
		case name == "":
			return nil, fmt.Errorf("label change %q: want +name or -name", tok)
		case strings.HasPrefix(tok, "+"):
			ops = append(ops, issuepkg.Op{Type: issuepkg.OpAddLabel, Value: name})
		case strings.HasPrefix(tok, "-"):
			ops = append(ops, issuepkg.Op{Type: issuepkg.OpRemoveLabel, Value: name})
		default:
			return nil, fmt.Errorf("label change %q: want +name or -name", tok)
		}
	}

	return ops, nil
}

func runLabel(ctx context.Context, client *git.Client, p recordPrompter, args []string) error {
	fetchIssues(ctx, client)

	var idArgs, tokens []string
	if len(args) > 0 {
		idArgs, tokens = args[:1], args[1:]
	}

	rec, err := resolveRecord(ctx, client, p, idArgs)
	if err != nil {
		return err
	}

	if len(tokens) == 0 {
		line, err := p.LabelChanges(ctx)
		if err != nil {
			return fmt.Errorf("label form: %w", err)
		}
		tokens = strings.Fields(line)
	}

	ops, err := parseLabelChanges(tokens)
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		return fmt.Errorf("no label change given for issue %s", rec.DisplayID())
	}

	for i := range ops {
		if err := issuepkg.Append(ctx, client, rec.ID, &ops[i]); err != nil {
			return fmt.Errorf("update labels: %w", err)
		}
	}

	pushIssue(ctx, client, rec.ID)

	updated, err := issuepkg.Load(ctx, client, rec.ID)
	if err != nil {
		return fmt.Errorf("reload issue: %w", err)
	}

	fmt.Fprintf(client.IO().Out, "Issue %s labels: %s\n", updated.DisplayID(), strings.Join(updated.Labels, ", "))

	return nil
}
