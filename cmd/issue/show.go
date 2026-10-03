package issue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/spf13/cobra"
)

const showTimeLayout = "2006-01-02 15:04"

func (i Issue) getShowCmd() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "show [<id>]",
		Short: "Show an issue stored in the repository and its comments",
		Long: `Show an issue stored in the repository. <id> is the full ID or a unique
prefix of at least 4 characters. Without <id> a picker lists the issues.`,
		Args: cobra.MaximumNArgs(1),
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the issue as JSON")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return i.showRunE(cmd, args, jsonOut)
	}

	return cmd
}

func (i Issue) showRunE(cmd *cobra.Command, args []string, jsonOut bool) error {
	client, err := cmdutil.NewClientForCmd(cmd, i.appConfig)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}

	return runShow(cmd.Context(), client, huhRecordPrompter{}, args, jsonOut)
}

func runShow(ctx context.Context, client *git.Client, p recordPrompter, args []string, jsonOut bool) error {
	fetchIssues(ctx, client)

	rec, err := resolveRecord(ctx, client, p, args)
	if err != nil {
		return err
	}

	if jsonOut {
		if err := json.NewEncoder(client.IO().Out).Encode(rec); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}

		return nil
	}

	renderRecord(client.IO().Out, &rec)

	return nil
}

func renderRecord(w io.Writer, rec *issuepkg.Record) {
	fmt.Fprintf(w, "%s  %s\n", rec.DisplayID(), rec.Title)
	fmt.Fprintf(w, "State: %s   Type: %s   Created: %s\n",
		rec.State, rec.BranchType, rec.CreatedAt.Local().Format(showTimeLayout))
	if len(rec.Labels) > 0 {
		fmt.Fprintf(w, "Labels: %s\n", strings.Join(rec.Labels, ", "))
	}
	fmt.Fprintf(w, "ID: %s\n", rec.ID)

	if rec.Description != "" {
		fmt.Fprintf(w, "\n%s\n", rec.Description)
	}

	for _, c := range rec.Comments {
		fmt.Fprintf(w, "\n--- %s  %s\n%s\n", c.At.Local().Format(showTimeLayout), c.Author, c.Body)
	}
}
