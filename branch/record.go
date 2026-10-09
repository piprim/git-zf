package branch

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/piprim/git-zf/internal/chain"
	"github.com/piprim/git-zf/internal/text"
)

// OpVersion is the op.json schema version this binary writes and understands.
const OpVersion = 1

// Op types. Fold skips any type it does not know, so an older binary tolerates
// ops written by a newer one.
const (
	OpStart     = "start"
	OpSetStatus = "set_status"
	OpMerge     = "merge"
)

// Statuses of a tracked branch. StatusAll is the "no filter" value of Rows,
// never a stored status.
const (
	StatusInProgress = "in_progress"
	StatusMerged     = "merged"
	StatusClosed     = "closed"
	StatusAll        = ""
)

// Op is one action on the branches of an issue: the content of op.json in one
// commit of the chain at refs/zf/branches/<slug>. ID and Parents come from the
// commit itself and are not part of the JSON.
type Op struct {
	V      int    `json:"v"`
	Type   string `json:"type"`
	At     string `json:"at"` // RFC 3339, UTC
	Author string `json:"author,omitempty"`

	Branch string `json:"branch,omitempty"` // start, set_status: the full branch name

	BranchType  string `json:"branch_type,omitempty"`  // start
	Title       string `json:"title,omitempty"`        // start: the issue subject
	Parent      string `json:"parent,omitempty"`       // start: slug of the parent issue
	TrackerType string `json:"tracker_type,omitempty"` // start: "" for a manual issue
	IssueID     string `json:"issue_id,omitempty"`     // start: full ID of the repo issue

	Status string `json:"status,omitempty"` // set_status

	ID      string   `json:"-"`
	Parents []string `json:"-"`
}

// DecodeOp builds the Op of commit id from its op.json payload. ok is false
// when the payload is missing, is not valid JSON or has an unknown version; the
// returned Op then has an empty Type, so Fold ignores it while its ID and
// Parents still keep the chain connected. The text fields are cleaned of
// control characters: the chain may come from another clone.
func DecodeOp(id string, parents []string, payload []byte) (op Op, ok bool) {
	if err := json.Unmarshal(payload, &op); err != nil || op.V != OpVersion {
		return Op{ID: id, Parents: parents}, false
	}

	op.ID, op.Parents = id, parents
	op.Author, op.Branch, op.BranchType, op.Title = text.Line(op.Author), text.Line(op.Branch), text.Line(op.BranchType), text.Line(op.Title)
	op.Parent, op.TrackerType, op.Status = text.Line(op.Parent), text.Line(op.TrackerType), text.Line(op.Status)

	return op, true
}

// Entry is one tracked branch of an issue.
type Entry struct {
	Name      string // full branch name
	Type      string
	Status    string    // StatusInProgress, StatusMerged or StatusClosed
	Author    string    // author of the start op
	CreatedAt time.Time // at of the start op
	UpdatedAt time.Time // at of the last applied op naming this branch
}

// State is what the chain of one issue slug records: the fold of its ops.
type State struct {
	Slug        string
	Title       string
	TrackerType string // "" for a manual issue
	IssueID     string // full ID of the repo issue, "" when there is none
	Parent      string // slug of the parent issue, "" when there is none
	Entries     []Entry

	// Warnings lists the commits Load skipped as malformed.
	Warnings []string
}

// Entry returns the entry of the branch called name, or nil.
func (st *State) Entry(name string) *Entry {
	for i := range st.Entries {
		if st.Entries[i].Name == name {
			return &st.Entries[i]
		}
	}

	return nil
}

// Fold computes the State of slug from its ops. The order of ops in the slice
// does not matter: they are linearized from their Parents.
func Fold(slug string, ops []Op) State {
	st := State{Slug: slug}

	ordered := chain.Order(ops, func(op *Op) chain.Node {
		return chain.Node{ID: op.ID, Parents: op.Parents, At: op.At}
	})
	for i := range ordered {
		apply(&st, &ordered[i])
	}

	return st
}

// apply applies op to st. An op that does not fit is ignored: that is how
// concurrent actions resolve the same way on every clone.
func apply(st *State, op *Op) {
	at := chain.ParseAt(op.At)

	switch op.Type {
	case OpStart:
		if op.Branch == "" {
			return
		}
		// The issue-level fields keep their first value, so they do not flip
		// after a sync.
		st.Title = cmp.Or(st.Title, op.Title)
		st.TrackerType = cmp.Or(st.TrackerType, op.TrackerType)
		st.IssueID = cmp.Or(st.IssueID, op.IssueID)
		st.Parent = cmp.Or(st.Parent, op.Parent)

		// A second start of a known branch (two clones tracked it) adds
		// nothing and reopens nothing: reopening is a set_status.
		if st.Entry(op.Branch) != nil {
			return
		}
		st.Entries = append(st.Entries, Entry{
			Name: op.Branch, Type: op.BranchType, Status: StatusInProgress,
			Author: op.Author, CreatedAt: at, UpdatedAt: at,
		})
	case OpSetStatus:
		e := st.Entry(op.Branch)
		if e == nil {
			return
		}
		switch op.Status {
		case StatusInProgress, StatusMerged, StatusClosed:
			e.Status, e.UpdatedAt = op.Status, at
		}
	}
}

// Row is one tracked branch with its issue: what commands list and pick from.
type Row struct {
	IssueSlug  string    `json:"issue_slug"`
	Title      string    `json:"title"`
	BranchName string    `json:"branch_name"`
	Type       string    `json:"type"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	Author     string    `json:"-"` // who wrote the branch's start op
}

// Rows flattens states into one row per branch, newest first. status keeps
// only the branches in that status; StatusAll keeps every one.
func Rows(states []State, status string) []Row {
	rows := []Row{}
	for i := range states {
		st := &states[i]
		for _, e := range st.Entries {
			if status != StatusAll && e.Status != status {
				continue
			}
			rows = append(rows, Row{
				IssueSlug: st.Slug, Title: cmp.Or(st.Title, TitleFromName(e.Name), st.Slug),
				BranchName: e.Name, Type: e.Type, Status: e.Status, CreatedAt: e.CreatedAt, Author: e.Author,
			})
		}
	}

	slices.SortStableFunc(rows, func(a, b Row) int {
		return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), cmp.Compare(a.BranchName, b.BranchName))
	})

	return rows
}

// TitleFromName derives a readable title from a branch name's slug segment:
// "42@feat@add-login" gives "add login". It returns "" for a name that is not
// a git-zf branch name.
func TitleFromName(name string) string {
	b, err := Parse(name)
	if err != nil {
		return ""
	}

	return strings.ReplaceAll(b.Title(), "-", " ")
}

// Children returns the states whose parent is slug.
func Children(states []State, slug string) []State {
	var children []State
	for i := range states {
		if states[i].Parent == slug {
			children = append(children, states[i])
		}
	}

	return children
}
