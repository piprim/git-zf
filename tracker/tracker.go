package tracker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/internal/text"
)

// Issue is the tracker-agnostic wire shape of a work item: every field is a
// string exactly as a tracker backend reports it. It is the external/source
// representation, the first of three "Issue" shapes — it is embedded into the
// in-flow domain entity issue.Issue, which is in turn persisted as store.Issue.
// See the doc on issue.Issue for the full picture.
type Issue struct {
	TrackerType string
	ID          string
	Subject     string
	Description string
	Status      string
	Project     string
	// Labels are the issue's labels as the tracker names them. On Redmine
	// it is one name: the status.
	Labels []string
	// CreatedAt is when the tracker says the issue was created; zero when the
	// call that built the Issue does not report it. UpdatedAt is the tracker's
	// time of the last change of any kind on the issue.
	CreatedAt time.Time
	UpdatedAt time.Time
	// Closed reports the tracker's open/closed state.
	Closed bool
	// AssignedToMe reports whether the authenticated user is an assignee.
	AssignedToMe bool
}

// Clean returns i with the control characters dropped from what the tracker
// sent, so that a title or a status reaching the terminal cannot carry an
// escape sequence. Every adapter returns Issues through it.
func (i Issue) Clean() Issue {
	i.Subject, i.Status, i.Project = text.Line(i.Subject), text.Line(i.Status), text.Line(i.Project)
	i.Description = text.Clean(i.Description)
	i.Labels = slices.Clone(i.Labels)
	for k := range i.Labels {
		i.Labels[k] = text.Line(i.Labels[k])
	}

	return i
}

// Comment is one comment of a tracker issue.
type Comment struct {
	ID        string
	Author    string // the tracker login
	Body      string
	CreatedAt time.Time
}

// Clean drops the control characters, as Issue.Clean does.
func (c Comment) Clean() Comment {
	c.ID, c.Author, c.Body = text.Line(c.ID), text.Line(c.Author), text.Clean(c.Body)

	return c
}

// ErrIssueNotFound is returned by IsIssueClosed when the tracker has no record
// of the requested issueID (HTTP 404 or equivalent). Callers can branch on it
// via errors.Is to distinguish "tracker says open" from "tracker doesn't know".
var ErrIssueNotFound = errors.New("tracker: issue not found")

// Tracker is the contract every adapter must satisfy.
type Tracker interface {
	// ListIssues retrieves the open issues: every open issue of each
	// configured project, or, without a project, every open issue the
	// authenticated user can see across the tracker (on GitHub, the ones
	// involving the user). It is the one listing of the commands and of the
	// issue mirror.
	ListIssues(ctx context.Context) ([]Issue, error)
	// ListStatuses returns the available status names for the tracker.
	ListStatuses(ctx context.Context) ([]string, error)
	// UpdateIssueStatus updates the status from the given issueID
	UpdateIssueStatus(ctx context.Context, issueID, statusName string) error
	// IsIssueClosed reports whether the tracker considers issueID closed.
	// Returns ErrIssueNotFound for missing-issue cases so callers can format
	// the warning distinctly from transport/auth failures.
	IsIssueClosed(ctx context.Context, issueID string) (bool, error)
	// AddComment posts body as a comment on issueID and returns the comment
	// with the ID the tracker gave it.
	AddComment(ctx context.Context, issueID, body string) (Comment, error)
	// GetIssue reads one issue: title, description, labels, updated time and
	// state. ErrIssueNotFound when the tracker has no such issue.
	GetIssue(ctx context.Context, issueID string) (Issue, error)
	// UpdateIssue sets the title and the description.
	UpdateIssue(ctx context.Context, issueID, title, description string) error
	// SetLabels replaces the issue's labels, creating the names the tracker
	// lacks. A set the tracker refuses by rule is ErrLabelRule.
	SetLabels(ctx context.Context, issueID string, labels []string) error
	// ListLabels returns the label names the tracker project defines.
	ListLabels(ctx context.Context) ([]string, error)
	// ListComments returns the issue's comments, oldest first.
	ListComments(ctx context.Context, issueID string) ([]Comment, error)
	// CreateIssue creates an issue in the single configured project and
	// returns it with the ID and status the tracker gave it.
	CreateIssue(ctx context.Context, title, description string) (Issue, error)
	// SetIssueOpen reopens (open) or closes issueID, with whatever status the
	// tracker uses for that.
	SetIssueOpen(ctx context.Context, issueID string, open bool) error
}

// ErrLabelRule is returned by SetLabels when the set breaks a rule of the
// tracker (Redmine: exactly one name, a status), as opposed to a transport
// or auth failure. The mirror then takes the tracker's labels back.
var ErrLabelRule = errors.New("tracker: label set breaks a rule of the tracker")

// SingleLabel reports whether t allows exactly one label per issue, which is
// its status. Redmine does; a tracker without the optional method does not.
func SingleLabel(t Tracker) bool {
	s, ok := t.(interface{ SingleLabel() bool })

	return ok && s.SingleLabel()
}

var registry = make(map[string]func(config.IssueTrackerConfig) (Tracker, error))

// Register adds a factory function for the named tracker type. It is meant to
// be called from an adapter's init(), before any New; it is not safe to call
// concurrently with New.
func Register(name string, fn func(config.IssueTrackerConfig) (Tracker, error)) {
	registry[name] = fn
}

// New constructs a Tracker from cfg using the registered factory.
func New(cfg config.IssueTrackerConfig) (Tracker, error) {
	fn, ok := registry[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unknown tracker type %q: adapter not registered", cfg.Type)
	}

	return fn(cfg)
}

// PullRequest is one pull request of the forge, as it names things.
type PullRequest struct {
	Number string // the forge's number, "12"
	URL    string
	Head   string // branch name
	Base   string
}

// Clean drops the control characters, as Issue.Clean does, and the blanks at
// the ends a newline leaves behind.
func (p PullRequest) Clean() PullRequest {
	clean := func(s string) string { return strings.TrimSpace(text.Line(s)) }
	p.Number = clean(p.Number)
	p.URL = clean(p.URL)
	p.Head = clean(p.Head)
	p.Base = clean(p.Base)

	return p
}

// MergeMethod is how the forge merges a pull request.
type MergeMethod string

const (
	MergeMethodMerge  MergeMethod = "merge"
	MergeMethodSquash MergeMethod = "squash"
	MergeMethodRebase MergeMethod = "rebase"
)

// ErrNoPullRequest is returned by FindPullRequest when the branch has no open
// pull request.
var ErrNoPullRequest = errors.New("tracker: no open pull request")

// PullRequests is the optional capability of a tracker that is also the forge
// hosting the repository: GitHub and Forgejo. Discover it with PullRequestsOf.
type PullRequests interface {
	// FindPullRequest returns the open pull request whose head is branch.
	FindPullRequest(ctx context.Context, branch string) (PullRequest, error)
	// CreatePullRequest opens one and returns it with its number and URL.
	CreatePullRequest(ctx context.Context, head, base, title, body string) (PullRequest, error)
	// MergePullRequest merges number with method, provided the pull request's
	// head is still head (the commit hash the caller verified); the forge
	// refuses when the head moved. A refusal of the forge (checks failing,
	// not mergeable, head moved) is an error carrying its message.
	MergePullRequest(ctx context.Context, number string, method MergeMethod, head string) error
}

// PullRequestsOf returns t's capability, or nil when the tracker has none
// (or t is nil).
func PullRequestsOf(t Tracker) PullRequests {
	p, ok := t.(PullRequests)
	if !ok {
		return nil
	}

	return p
}
