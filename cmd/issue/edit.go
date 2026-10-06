package issue

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/spf13/cobra"
)

// editFlags carries --title and --description of `issue edit`. A nil field is
// a flag that was not passed.
type editFlags struct {
	title, description *string
}

func (i Issue) getEditCmd() *cobra.Command {
	var title, description string

	cmd := &cobra.Command{
		Use:   "edit [<id>]",
		Short: "Edit the title and description of an issue stored in the repository",
		Long: `Change the title and the description of an issue stored in the repository
and push it. <id> is the full ID or a unique prefix of at least 4 characters.
Without <id> a picker lists the issues.

--title and --description skip the form and change only the field passed;
--description "" clears the description. A branch already started for the
issue keeps its name.`,
		Args: cobra.MaximumNArgs(1),
	}

	f := cmd.Flags()
	f.StringVar(&title, "title", "", "new title (skips the form)")
	f.StringVar(&description, "description", "", "new description (skips the form)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Changed and not "non-empty": --description "" clears the field. On the
		// issue menu's parent command, which lacks both flags, it reads false.
		var flags editFlags
		if cmd.Flags().Changed("title") {
			flags.title = &title
		}
		if cmd.Flags().Changed("description") {
			flags.description = &description
		}

		return i.editRunE(cmd, args, flags)
	}

	return cmd
}

func (i Issue) editRunE(cmd *cobra.Command, args []string, flags editFlags) error {
	client, err := cmdutil.NewClientForCmd(cmd, i.appConfig)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}

	return runEdit(cmd.Context(), client, huhRecordPrompter{}, args, flags)
}

// runEdit changes the title and the description of an issue. Without flags
// the form asks for both. Only a field that changed gets an op.
func runEdit(ctx context.Context, client *git.Client, p recordPrompter, args []string, flags editFlags) error {
	fetchIssues(ctx, client)

	rec, err := resolveRecord(ctx, client, p, args)
	if err != nil {
		return err
	}

	newTitle, newDescription := rec.Title, rec.Description
	if flags.title == nil && flags.description == nil {
		if err := p.EditIssue(ctx, &newTitle, &newDescription); err != nil {
			return fmt.Errorf("edit form: %w", err)
		}
	}
	if flags.title != nil {
		newTitle = *flags.title
	}
	if flags.description != nil {
		newDescription = *flags.description
	}

	newTitle = strings.TrimSpace(newTitle)
	if newTitle == "" {
		return errors.New("issue title is required")
	}

	var ops []issuepkg.Op
	if newTitle != rec.Title {
		ops = append(ops, issuepkg.Op{Type: issuepkg.OpSetTitle, Value: newTitle})
	}
	if newDescription != rec.Description {
		ops = append(ops, issuepkg.Op{Type: issuepkg.OpSetDescription, Value: newDescription})
	}

	if len(ops) == 0 {
		fmt.Fprintf(client.IO().Out, "Issue %s unchanged\n", rec.DisplayID())

		return nil
	}

	for i := range ops {
		if err := issuepkg.Append(ctx, client, rec.ID, &ops[i]); err != nil {
			return fmt.Errorf("edit issue: %w", err)
		}
	}

	pushIssue(ctx, client, rec.ID)
	fmt.Fprintf(client.IO().Out, "Edited issue %s: %s\n", rec.DisplayID(), newTitle)

	return nil
}
