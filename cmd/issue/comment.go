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

func (i Issue) getCommentCmd() *cobra.Command {
	var message string

	cmd := &cobra.Command{
		Use:   "comment [<id>]",
		Short: "Comment on an issue stored in the repository",
		Long: `Add a comment to an issue stored in the repository and push it. <id> is
the full ID or a unique prefix of at least 4 characters. Without <id> a picker
lists the issues. --message skips the comment form.`,
		Args: cobra.MaximumNArgs(1),
	}

	cmd.Flags().StringVarP(&message, "message", "m", "", "comment text (skips the form)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return i.commentRunE(cmd, args, message)
	}

	return cmd
}

func (i Issue) commentRunE(cmd *cobra.Command, args []string, message string) error {
	client, err := cmdutil.NewClientForCmd(cmd, i.appConfig)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}

	return runComment(cmd.Context(), client, huhRecordPrompter{}, args, message)
}

func runComment(ctx context.Context, client *git.Client, p recordPrompter, args []string, message string) error {
	fetchIssues(ctx, client)

	rec, err := resolveRecord(ctx, client, p, args)
	if err != nil {
		return err
	}

	body := message
	if body == "" {
		body, err = p.CommentBody(ctx)
		if err != nil {
			return fmt.Errorf("comment form: %w", err)
		}
	}

	body = strings.TrimSpace(body)
	if body == "" {
		return errors.New("comment is empty")
	}

	if err := issuepkg.Append(ctx, client, rec.ID, &issuepkg.Op{Type: issuepkg.OpAddComment, Body: body}); err != nil {
		return fmt.Errorf("add comment: %w", err)
	}

	pushIssue(ctx, client, rec.ID)
	fmt.Fprintf(client.IO().Out, "Commented on issue %s\n", rec.DisplayID())

	return nil
}
