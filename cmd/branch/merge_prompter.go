package branch

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/cmd/mergeflow"
	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
)

// SourceBranch is one pickable merge source. RemoteOnly marks a branch that
// exists on origin but not locally, so the picker can label it and runMerge
// knows to materialize it before merging.
type SourceBranch struct {
	Name       string
	RemoteOnly bool
}

// MergePrompter resolves every user-facing decision in the branch-merge flow.
// huhMergePrompter drives huh forms; merge_prompter_test.go provides a scripted
// implementation for the E2E tests.
type MergePrompter interface {
	mergeflow.Prompter // PickStrategy, ConfirmMerge, ComposeMessage
	PickSource(ctx context.Context, sources []SourceBranch) (SourceBranch, error)
	ConfirmDeleteSource(ctx context.Context, source string) (delete bool, err error)
}

var _ MergePrompter = (*huhMergePrompter)(nil)

// huhMergePrompter is the production MergePrompter. It reuses the same tui form
// constructors `issue close` uses for the strategy pick, confirm, and commit
// message, so branch merge and issue close present an identical merge UX.
type huhMergePrompter struct {
	client *git.Client
	store  *store.Store
	cfg    *config.AppConfig
}

func newHuhMergePrompter(c *git.Client, s *store.Store, cfg *config.AppConfig) *huhMergePrompter {
	return &huhMergePrompter{client: c, store: s, cfg: cfg}
}

func (p *huhMergePrompter) PickSource(ctx context.Context, sources []SourceBranch) (SourceBranch, error) {
	opts := make([]huh.Option[string], 0, len(sources))
	byName := make(map[string]SourceBranch, len(sources))
	for _, s := range sources {
		label := s.Name
		if s.RemoteOnly {
			label = s.Name + " (origin)"
		}
		opts = append(opts, huh.NewOption(label, s.Name))
		byName[s.Name] = s
	}

	var picked string
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Branch to merge into current:").
			Options(opts...).
			Value(&picked),
	))
	if err := form.RunWithContext(ctx); err != nil {
		return SourceBranch{}, fmt.Errorf("source picker: %w", err)
	}

	return byName[picked], nil
}

func (p *huhMergePrompter) PickStrategy(ctx context.Context) (commit.MergeStrategy, error) {
	var picked string
	form := tui.IssueMergeStrategy(&picked, []tui.StrategyOption{
		{
			Value: string(commit.MergeStrategyRebase),
			Label: "Rebase",
			Hint:  "Single clean commit on current, submodule-safe (recommended)",
		},
		{
			Value: string(commit.MergeStrategySquash),
			Label: "Squash",
			Hint:  "git merge --squash — fast, but not submodule-safe",
		},
		{
			Value: string(commit.MergeStrategyClassic),
			Label: "Classic",
			Hint:  "git merge --no-ff with commitizen message — preserves full history",
		},
	})
	if err := huh.NewForm(form).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("strategy picker: %w", err)
	}

	return commit.MergeStrategy(picked), nil
}

func (p *huhMergePrompter) ConfirmMerge(ctx context.Context, source, target string, s commit.MergeStrategy) (bool, error) {
	var confirmed bool
	if err := huh.NewForm(tui.IssueMergeConfirm(source, target, string(s), &confirmed)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm form: %w", err)
	}

	return confirmed, nil
}

func (p *huhMergePrompter) ComposeMessage(ctx context.Context, prefill map[string]any) ([]byte, tui.CommitOption, error) {
	authors, err := p.client.Authors(ctx)
	if err != nil {
		slog.Warn("could not load author list", "error", err)

		authors = []string{}
	}

	defaults := tui.CommitOption{Authors: authors}
	if len(authors) > 0 {
		defaults.Author = authors[0]
	}

	msg, opts, err := commit.FillOutForm(ctx, p.cfg, defaults, p.store, prefill, nil)
	if err != nil {
		return nil, tui.CommitOption{}, fmt.Errorf("fill commit form: %w", err)
	}

	return msg, opts, nil
}

func (p *huhMergePrompter) ConfirmDeleteSource(ctx context.Context, source string) (bool, error) {
	var del bool
	if err := huh.NewForm(tui.IssueDeleteBranch(source, &del)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("delete branch form: %w", err)
	}

	return del, nil
}
