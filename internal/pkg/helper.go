package pkg

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

func BranchFieldOrEmpty(b *store.BranchRow, fn func(*store.BranchRow) string) string {
	if b == nil {
		return "∅"
	}

	return fn(b)
}

func TrackerStatusOrNA(s *string) string {
	if s == nil {
		return "N.A."
	}

	return *s
}

func GetAllowedBranchType(types []config.CommitTypeOption) []string {
	allowedBranchTypes := make([]string, 0, len(types))
	for _, t := range types {
		allowedBranchTypes = append(allowedBranchTypes, t.Name)
	}

	return allowedBranchTypes
}

func GetStore(ctx context.Context) (*store.Store, error) {
	client, err := git.NewClient()
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}

	root, err := client.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	s, err := store.Open(ctx, filepath.Join(root, ".git"))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	return s, nil
}
