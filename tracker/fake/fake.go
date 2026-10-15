// Package fake provides an in-process tracker.Tracker for tests.
// It registers itself under type "fake" so close_e2e_test.go can wire it
// through config.IssueTrackerConfig like a real adapter.
package fake

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
)

// fakeEpoch is where the fake's clock starts; every mutation adds a second.
var fakeEpoch = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func init() {
	tracker.Register("fake", New)
}

// Tracker is the exposed concrete type so tests can read RecordedUpdates
// after Close() returns. Construct via New() to satisfy the registry signature.
type Tracker struct {
	mu               sync.Mutex
	Statuses         []string
	RecordedUpdates  []Update
	RecordedComments []Comment

	// Closed[id] == true → IsIssueClosed returns (true, nil) for id.
	// Default zero-value (absent or false) → IsIssueClosed returns (false, nil).
	Closed map[string]bool
	// Unknown[id] == true → IsIssueClosed returns (false, tracker.ErrIssueNotFound).
	Unknown map[string]bool
	// Errors[id] != nil → IsIssueClosed returns (false, Errors[id]) — for transport-error scenarios.
	Errors map[string]error

	// ProjectIssues is what ListIssues returns: the project's open issues.
	// CreateIssue, SetIssueOpen, CloseIssue and ReopenIssue keep it and
	// Closed in step, so the fake behaves like one tracker over time.
	ProjectIssues []tracker.Issue
	// ListErr and CreateErr, when non-nil, are returned by ListIssues and
	// CreateIssue.
	ListErr, CreateErr error
	// ListCalls counts the ListIssues calls.
	ListCalls       int
	RecordedCreates []Create
	RecordedOpens   []Open
	// ClosingStatuses are the status names that close an issue when
	// UpdateIssueStatus applies them. Any other name becomes the issue's
	// listed status.
	ClosingStatuses []string

	// Labels is the catalogue of the project's labels; SetLabels adds the
	// names it lacks. Comments holds each issue's comments by issue ID.
	Labels   []string
	Comments map[string][]tracker.Comment
	// StatusIsLabel makes the fake behave like Redmine: an issue's Labels is
	// its status, SetLabels sets the status or returns ErrLabelRule, and
	// SingleLabel is true.
	StatusIsLabel bool
	// GetErr, when non-nil, is returned by GetIssue.
	GetErr            error
	RecordedEdits     []Edit
	RecordedLabelSets []LabelSet
	// SetLabelsErr, when non-nil, is returned by SetLabels.
	SetLabelsErr error
	// ListLabelsErr, when non-nil, is returned by ListLabels; ListLabelsCalls
	// counts the calls.
	ListLabelsErr   error
	ListLabelsCalls int

	// PullRequests are the forge's open pull requests; FindPullRequest
	// matches their Head. CreatePullRequest appends, MergePullRequest
	// removes.
	PullRequests         []tracker.PullRequest
	RecordedPullRequests []PullRequestCreate
	RecordedMerges       []Merge
	// MergeError, when non-nil, is returned by MergePullRequest: the forge
	// refuses the merge (checks failing, not mergeable).
	MergeError error

	ticks   int
	nextCID int
	nextID  int
	shelved map[string]tracker.Issue // closed issues, by ID
}

// Update captures one UpdateIssueStatus call.
type Update struct {
	IssueID    string
	StatusName string
}

// Edit captures one UpdateIssue call.
type Edit struct {
	IssueID, Title, Description string
}

// PullRequestCreate captures one CreatePullRequest call.
type PullRequestCreate struct {
	Head, Base, Title, Body string
}

// Merge captures one MergePullRequest call.
type Merge struct {
	Number string
	Method tracker.MergeMethod
}

// LabelSet captures one SetLabels call.
type LabelSet struct {
	IssueID string
	Labels  []string
}

// Comment captures one AddComment call.
type Comment struct {
	IssueID string
	ID      string
	Body    string
}

// New is the tracker.Register factory. cfg is ignored — tests configure the
// returned *Tracker directly via field access.
func New(_ config.IssueTrackerConfig) (tracker.Tracker, error) {
	return &Tracker{
		Statuses: []string{"In Progress", "Closed"},
	}, nil
}

// ListStatuses returns a snapshot of the configured status names.
func (t *Tracker) ListStatuses(_ context.Context) ([]string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]string, len(t.Statuses))
	copy(out, t.Statuses)

	return out, nil
}

// IsIssueClosed consults Closed / Unknown / Errors in that priority order.
func (t *Tracker) IsIssueClosed(_ context.Context, issueID string) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err, ok := t.Errors[issueID]; ok && err != nil {
		return false, err
	}

	if t.Unknown[issueID] {
		return false, tracker.ErrIssueNotFound
	}

	return t.Closed[issueID], nil
}

// UpdateIssueStatus records the call so tests can assert on it.
func (t *Tracker) UpdateIssueStatus(_ context.Context, issueID, statusName string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.RecordedUpdates = append(t.RecordedUpdates, Update{IssueID: issueID, StatusName: statusName})

	if slices.Contains(t.ClosingStatuses, statusName) {
		t.mutate(issueID, func(iss *tracker.Issue) { iss.Status = statusName })
		t.setOpen(issueID, false)

		return nil
	}

	t.mutate(issueID, func(iss *tracker.Issue) { iss.Status = statusName })

	return nil
}

// Create captures one CreateIssue call.
type Create struct {
	Title, Description string
}

// Open captures one SetIssueOpen call.
type Open struct {
	IssueID string
	Open    bool
}

// ListIssues returns a snapshot of the project's open issues.
func (t *Tracker) ListIssues(_ context.Context) ([]tracker.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.ListCalls++
	if t.ListErr != nil {
		return nil, t.ListErr
	}

	out := make([]tracker.Issue, len(t.ProjectIssues))
	for i, iss := range t.ProjectIssues {
		out[i] = t.view(iss, false)
	}

	return out, nil
}

// CreateIssue records the call and adds an open issue numbered 1, 2, 3….
func (t *Tracker) CreateIssue(_ context.Context, title, description string) (tracker.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.CreateErr != nil {
		return tracker.Issue{}, t.CreateErr
	}

	// The next free number: a seeded or shelved issue keeps its own, so a
	// test that seeds "1" never collides with a created issue.
	taken := t.numbersTaken()
	t.nextID++
	for taken[t.nextID] {
		t.nextID++
	}
	iss := tracker.Issue{
		TrackerType: "fake", ID: strconv.Itoa(t.nextID), Subject: title, Description: description,
		Status: "open", CreatedAt: time.Now().UTC(), UpdatedAt: t.now(),
	}
	if t.StatusIsLabel {
		iss.Status = t.firstOpenStatus()
	}
	t.RecordedCreates = append(t.RecordedCreates, Create{Title: title, Description: description})
	t.ProjectIssues = append(t.ProjectIssues, iss)

	return t.view(iss, false), nil
}

// numbersTaken is the set of numeric IDs of the listed and shelved issues.
// The caller holds t.mu.
func (t *Tracker) numbersTaken() map[int]bool {
	taken := make(map[int]bool, len(t.ProjectIssues)+len(t.shelved))
	for _, iss := range t.ProjectIssues {
		if n, err := strconv.Atoi(iss.ID); err == nil {
			taken[n] = true
		}
	}
	for id := range t.shelved {
		if n, err := strconv.Atoi(id); err == nil {
			taken[n] = true
		}
	}

	return taken
}

// SetIssueOpen records the call and opens or closes the issue.
func (t *Tracker) SetIssueOpen(_ context.Context, issueID string, open bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.RecordedOpens = append(t.RecordedOpens, Open{IssueID: issueID, Open: open})
	t.setOpen(issueID, open)

	return nil
}

// CloseIssue closes the issue the way a person does in the tracker's own UI:
// nothing is recorded.
func (t *Tracker) CloseIssue(issueID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.setOpen(issueID, false)
}

// ReopenIssue is the counterpart of CloseIssue.
func (t *Tracker) ReopenIssue(issueID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.setOpen(issueID, true)
}

// setOpen moves the issue between ProjectIssues and the closed shelf. The
// caller holds t.mu.
func (t *Tracker) setOpen(issueID string, open bool) {
	if t.Closed == nil {
		t.Closed = make(map[string]bool)
	}
	if t.shelved == nil {
		t.shelved = make(map[string]tracker.Issue)
	}

	t.Closed[issueID] = !open

	i := slices.IndexFunc(t.ProjectIssues, func(iss tracker.Issue) bool { return iss.ID == issueID })
	switch {
	case !open && i >= 0:
		iss := t.ProjectIssues[i]
		if !slices.Contains(t.ClosingStatuses, iss.Status) {
			iss.Status = "closed"
			if len(t.ClosingStatuses) > 0 {
				iss.Status = t.ClosingStatuses[0]
			}
		}
		iss.UpdatedAt = t.now()
		t.shelved[issueID] = iss
		t.ProjectIssues = slices.Delete(t.ProjectIssues, i, i+1)
	case open && i < 0:
		if iss, ok := t.shelved[issueID]; ok {
			// A reopened issue is "open"; with StatusIsLabel it takes the first
			// open status, as Redmine does.
			iss.Status = "open"
			if t.StatusIsLabel {
				iss.Status = t.firstOpenStatus()
			}
			iss.UpdatedAt = t.now()
			t.ProjectIssues = append(t.ProjectIssues, iss)
			delete(t.shelved, issueID)
		}
	}
}

// firstOpenStatus is the first status that does not close, "open" when
// there is none: what a created or reopened issue gets on Redmine.
func (t *Tracker) firstOpenStatus() string {
	for _, st := range t.Statuses {
		if !slices.Contains(t.ClosingStatuses, st) {
			return st
		}
	}

	return "open"
}

// now advances the fake's clock. The caller holds t.mu.
func (t *Tracker) now() time.Time {
	t.ticks++

	return fakeEpoch.Add(time.Duration(t.ticks) * time.Second)
}

// view is the issue as the tracker shows it: with its status as the label
// when StatusIsLabel. The caller holds t.mu.
func (t *Tracker) view(iss tracker.Issue, closed bool) tracker.Issue {
	iss.Closed = closed
	if t.StatusIsLabel {
		iss.Labels = []string{iss.Status}
	}

	return iss
}

// SingleLabel reports StatusIsLabel.
func (t *Tracker) SingleLabel() bool { return t.StatusIsLabel }

// Edit applies fn to the issue the way a person edits it on the web: not
// recorded, UpdatedAt moves.
func (t *Tracker) Edit(issueID string, fn func(*tracker.Issue)) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.mutate(issueID, fn)
}

// mutate applies fn to the listed or shelved issue and bumps UpdatedAt. The
// caller holds t.mu.
func (t *Tracker) mutate(issueID string, fn func(*tracker.Issue)) {
	for i := range t.ProjectIssues {
		if t.ProjectIssues[i].ID == issueID {
			fn(&t.ProjectIssues[i])
			t.ProjectIssues[i].UpdatedAt = t.now()

			return
		}
	}
	if iss, ok := t.shelved[issueID]; ok {
		fn(&iss)
		iss.UpdatedAt = t.now()
		t.shelved[issueID] = iss
	}
}

// GetIssue returns the issue, listed or closed, honouring Errors, Unknown
// and GetErr.
func (t *Tracker) GetIssue(_ context.Context, issueID string) (tracker.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.GetErr != nil {
		return tracker.Issue{}, t.GetErr
	}
	if err, ok := t.Errors[issueID]; ok && err != nil {
		return tracker.Issue{}, err
	}
	if t.Unknown[issueID] {
		return tracker.Issue{}, tracker.ErrIssueNotFound
	}
	for _, iss := range t.ProjectIssues {
		if iss.ID == issueID {
			return t.view(iss, false), nil
		}
	}
	if iss, ok := t.shelved[issueID]; ok {
		return t.view(iss, true), nil
	}

	return tracker.Issue{}, tracker.ErrIssueNotFound
}

// UpdateIssue records the call and sets title and description.
func (t *Tracker) UpdateIssue(_ context.Context, issueID, title, description string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.RecordedEdits = append(t.RecordedEdits, Edit{IssueID: issueID, Title: title, Description: description})
	t.mutate(issueID, func(iss *tracker.Issue) { iss.Subject, iss.Description = title, description })

	return nil
}

// ListLabels returns the catalogue, or the statuses with StatusIsLabel.
func (t *Tracker) ListLabels(_ context.Context) ([]string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ListLabelsCalls++

	if t.ListLabelsErr != nil {
		return nil, t.ListLabelsErr
	}
	if t.StatusIsLabel {
		return slices.Clone(t.Statuses), nil
	}

	return slices.Clone(t.Labels), nil
}

// SetLabels records the call and sets the issue's labels, adding the names
// the catalogue lacks. With StatusIsLabel it sets the status named by the one
// label, or returns ErrLabelRule.
func (t *Tracker) SetLabels(ctx context.Context, issueID string, labels []string) error {
	if t.SetLabelsErr != nil {
		return t.SetLabelsErr
	}

	t.mu.Lock()
	t.RecordedLabelSets = append(t.RecordedLabelSets, LabelSet{IssueID: issueID, Labels: slices.Clone(labels)})
	single := t.StatusIsLabel
	t.mu.Unlock()

	if single {
		if len(labels) != 1 {
			return fmt.Errorf("%w: fake wants exactly one label, got %d", tracker.ErrLabelRule, len(labels))
		}
		// Matched in any case and set in the tracker's spelling, as Redmine.
		i := slices.IndexFunc(t.Statuses, func(st string) bool { return strings.EqualFold(st, labels[0]) })
		if i < 0 {
			return fmt.Errorf("%w: %q is not a status", tracker.ErrLabelRule, labels[0])
		}

		return t.UpdateIssueStatus(ctx, issueID, t.Statuses[i])
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, name := range labels {
		if !slices.Contains(t.Labels, name) {
			t.Labels = append(t.Labels, name)
		}
	}
	t.mutate(issueID, func(iss *tracker.Issue) { iss.Labels = slices.Clone(labels) })

	return nil
}

// ListComments returns the issue's comments, oldest first.
func (t *Tracker) ListComments(_ context.Context, issueID string) ([]tracker.Comment, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	return slices.Clone(t.Comments[issueID]), nil
}

// AddComment records the call, lists the comment and returns it with its ID.
func (t *Tracker) AddComment(_ context.Context, issueID, body string) (tracker.Comment, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	c := t.addComment(issueID, "fake-user", body)
	t.RecordedComments = append(t.RecordedComments, Comment{IssueID: issueID, ID: c.ID, Body: body})

	return c, nil
}

// WebComment adds a comment the way a person does on the web: not recorded.
func (t *Tracker) WebComment(issueID, author, body string) tracker.Comment {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.addComment(issueID, author, body)
}

// addComment numbers the comment c1, c2, …, lists it and bumps the issue.
// The caller holds t.mu.
func (t *Tracker) addComment(issueID, author, body string) tracker.Comment {
	if t.Comments == nil {
		t.Comments = make(map[string][]tracker.Comment)
	}
	t.nextCID++
	c := tracker.Comment{ID: "c" + strconv.Itoa(t.nextCID), Author: author, Body: body, CreatedAt: t.now()}
	t.Comments[issueID] = append(t.Comments[issueID], c)
	t.mutate(issueID, func(*tracker.Issue) {})

	return c
}

// FindPullRequest returns the open pull request whose head is branch.
func (t *Tracker) FindPullRequest(_ context.Context, branch string) (tracker.PullRequest, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, pr := range t.PullRequests {
		if pr.Head == branch {
			return pr, nil
		}
	}

	return tracker.PullRequest{}, tracker.ErrNoPullRequest
}

// CreatePullRequest records the call and lists the pull request under the
// next free number.
func (t *Tracker) CreatePullRequest(_ context.Context, head, base, title, body string) (tracker.PullRequest, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	next := 1
	for _, pr := range t.PullRequests {
		if n, err := strconv.Atoi(pr.Number); err == nil && n >= next {
			next = n + 1
		}
	}
	num := strconv.Itoa(next)
	pr := tracker.PullRequest{Number: num, URL: "fake://pr/" + num, Head: head, Base: base}
	t.RecordedPullRequests = append(t.RecordedPullRequests, PullRequestCreate{Head: head, Base: base, Title: title, Body: body})
	t.PullRequests = append(t.PullRequests, pr)

	return pr, nil
}

// MergePullRequest records the merge and removes the pull request, or
// returns MergeError untouched.
func (t *Tracker) MergePullRequest(_ context.Context, number string, method tracker.MergeMethod) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.MergeError != nil {
		return t.MergeError
	}

	i := slices.IndexFunc(t.PullRequests, func(pr tracker.PullRequest) bool { return pr.Number == number })
	if i < 0 {
		return fmt.Errorf("fake: no open pull request #%s", number)
	}
	t.PullRequests = slices.Delete(t.PullRequests, i, i+1)
	t.RecordedMerges = append(t.RecordedMerges, Merge{Number: number, Method: method})

	return nil
}
