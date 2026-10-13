package issue

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
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
arguments a picker lists the issues, then a form lists the issue's labels to
untick, plus a line for new ones. With the issue mirror on, the list holds the
tracker's labels too; on Redmine the one label is the status, picked among the
tracker's statuses. "issue new" ends with the same form.`,
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

	return runLabel(cmd.Context(), client, huhRecordPrompter{}, args, openMirror(i.appConfig, client.IO().Err))
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

// applyLabelChanges applies add/remove ops to current and returns the sorted
// result.
func applyLabelChanges(current []string, ops []issuepkg.Op) []string {
	set := make(map[string]bool, len(current))
	for _, l := range current {
		set[l] = true
	}
	for _, op := range ops {
		if op.Type == issuepkg.OpAddLabel {
			set[op.Value] = true
		} else {
			delete(set, op.Value)
		}
	}
	final := make([]string, 0, len(set))
	for l := range set {
		final = append(final, l)
	}
	slices.Sort(final)

	return final
}

// diffLabels is the ops that take current to final.
func diffLabels(current, final []string) []issuepkg.Op {
	var ops []issuepkg.Op
	for _, l := range current {
		if !slices.Contains(final, l) {
			ops = append(ops, issuepkg.Op{Type: issuepkg.OpRemoveLabel, Value: l})
		}
	}
	for _, l := range final {
		if !slices.Contains(current, l) {
			ops = append(ops, issuepkg.Op{Type: issuepkg.OpAddLabel, Value: l})
		}
	}

	return ops
}

// labelChoices offers the tracker's labels when the mirror is on (asked
// once, see Mirror.Labels). nil when the mirror is off or the tracker does
// not answer: the form is then the text line and the tokens are not checked.
func labelChoices(ctx context.Context, client *git.Client, m *issuepkg.Mirror, current []string) (choices []string, single bool) {
	if m == nil {
		return nil, false
	}
	names, err := m.Labels(ctx)
	if err != nil {
		fmt.Fprintf(client.IO().Err, "warning: could not list the tracker's labels: %v\n", err)

		return nil, false
	}
	choices = issuepkg.CleanLabels(append(names, current...))
	single = tracker.SingleLabel(m.Tracker)
	if !single {
		slices.Sort(choices)
	}

	return choices, single
}

func runLabel(ctx context.Context, client *git.Client, p recordPrompter, args []string, m *issuepkg.Mirror) error {
	issueflow.FetchIssues(ctx, client)

	var idArgs, tokens []string
	if len(args) > 0 {
		idArgs, tokens = args[:1], args[1:]
	}

	return withRecord(ctx, client, p, idArgs, func(rec issuepkg.Record) error {
		choices, single := labelChoices(ctx, client, m, rec.Labels)

		var final []string
		if len(tokens) == 0 {
			var err error
			if final, err = p.LabelChanges(ctx, choices, rec.Labels, single); err != nil {
				return fmt.Errorf("label form: %w", err)
			}
		} else {
			changes, err := parseLabelChanges(tokens)
			if err != nil {
				return err
			}
			final = applyLabelChanges(rec.Labels, changes)
		}
		if single && choices != nil && (len(final) != 1 || !slices.Contains(choices, final[0])) {
			return fmt.Errorf("this tracker's one label is the status: want exactly one of %s, got %q", strings.Join(choices, ", "), final)
		}

		ops := diffLabels(rec.Labels, final)
		if len(ops) == 0 {
			return fmt.Errorf("no label change given for issue %s", rec.DisplayID())
		}

		for i := range ops {
			if err := issuepkg.Append(ctx, client, rec.ID, &ops[i]); err != nil {
				return fmt.Errorf("update labels: %w", err)
			}
		}

		pushIssue(ctx, client, rec.ID)
		syncOne(ctx, client, m, rec.ID)

		updated, err := issuepkg.Load(ctx, client, rec.ID)
		if err != nil {
			return fmt.Errorf("reload issue: %w", err)
		}

		fmt.Fprintf(client.IO().Out, "Issue %s labels: %s\n", updated.DisplayID(), strings.Join(updated.Labels, ", "))

		return nil
	})
}
