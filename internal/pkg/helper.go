package pkg

import (
	"github.com/piprim/git-zf/config"
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
