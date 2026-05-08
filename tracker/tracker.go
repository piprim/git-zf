package tracker

import (
	"context"
	"fmt"
	"sync"

	"github.com/piprim/git-zf/config"
)

// Issue is the tracker-agnostic representation of a work item.
type Issue struct {
	TrackerType string
	ID          string
	Subject     string
	Description string
	Status      string
	Project     string
}

// Tracker is the contract every adapter must satisfy.
type Tracker interface {
	// ListIssues retrieves the issues from the tracker
	ListIssues(ctx context.Context) ([]Issue, error)
	// ListStatuses returns the available status names for the tracker.
	ListStatuses(ctx context.Context) ([]string, error)
	// UpdateIssueStatus updates the status from the given issueID
	UpdateIssueStatus(ctx context.Context, issueID, statusName string) error
}

var (
	registryMu sync.RWMutex
	registry   = make(map[string]func(config.IssueTrackerConfig) (Tracker, error))
)

// Register adds a factory function for the named tracker type.
func Register(name string, fn func(config.IssueTrackerConfig) (Tracker, error)) {
	registryMu.Lock()
	defer registryMu.Unlock()

	registry[name] = fn
}

// New constructs a Tracker from cfg using the registered factory.
func New(cfg config.IssueTrackerConfig) (Tracker, error) {
	registryMu.RLock()
	fn, ok := registry[cfg.Type]
	registryMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown tracker type %q: adapter not registered", cfg.Type)
	}

	return fn(cfg)
}
