package issue

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/piprim/git-zf/internal/chain"
	"github.com/piprim/git-zf/internal/text"
)

// OpVersion is the op.json schema version this binary writes and understands.
const OpVersion = 1

// Op types. Fold skips any type it does not know, so an older binary tolerates
// ops written by a newer one.
const (
	OpCreate         = "create"
	OpSetTitle       = "set_title"
	OpSetDescription = "set_description"
	OpSetState       = "set_state"
	OpAddLabel       = "add_label"
	OpRemoveLabel    = "remove_label"
	OpAddComment     = "add_comment"
	OpMerge          = "merge"
	OpLinkTracker    = "link_tracker"
	OpTrackerState   = "tracker_state"
)

// Issue states.
const (
	StateOpen   = "open"
	StateClosed = "closed"
)

const shortIDLen = 7

// Op is one change to an issue: the content of op.json in one commit of the
// chain at refs/zf/issues/<id>. ID and Parents come from the commit itself and
// are not part of the JSON.
type Op struct {
	V      int    `json:"v"`
	Type   string `json:"type"`
	At     string `json:"at"` // RFC 3339, UTC
	Author string `json:"author,omitempty"`

	Title       string `json:"title,omitempty"`       // create
	Description string `json:"description,omitempty"` // create
	BranchType  string `json:"branch_type,omitempty"` // create
	Value       string `json:"value,omitempty"`       // set_title, set_description, set_state, add_label, remove_label
	Body        string `json:"body,omitempty"`        // add_comment

	TrackerType string `json:"tracker_type,omitempty"` // create (imported issue), link_tracker
	Project     string `json:"project,omitempty"`      // create, link_tracker: the configured project name
	TrackerID   string `json:"tracker_id,omitempty"`   // create, link_tracker: the tracker's issue number
	Status      string `json:"status,omitempty"`       // tracker_state: the tracker's status name

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
	op.Author, op.Title, op.BranchType = text.Line(op.Author), text.Line(op.Title), text.Line(op.BranchType)
	op.TrackerType, op.Project, op.TrackerID, op.Status =
		text.Line(op.TrackerType), text.Line(op.Project), text.Line(op.TrackerID), text.Line(op.Status)
	op.Description, op.Body = text.Clean(op.Description), text.Clean(op.Body)
	if op.Type == OpSetDescription {
		op.Value = text.Clean(op.Value)
	} else {
		op.Value = text.Line(op.Value)
	}

	return op, true
}

// Comment is one add_comment op.
type Comment struct {
	ID     string    `json:"id"`
	Author string    `json:"author"`
	At     time.Time `json:"at"`
	Body   string    `json:"body"`
}

// TrackerLink names the tracker issue a record is mirrored with.
type TrackerLink struct {
	Type    string `json:"type"`    // tracker type, e.g. "forgejo"
	Project string `json:"project"` // the configured project name
	ID      string `json:"id"`      // the tracker's issue number
	// Born is true when the link comes from the create op: the issue was
	// imported from the tracker.
	Born bool `json:"born"`
}

// Record is the current state of an issue: the fold of its op chain.
type Record struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	BranchType  string    `json:"branch_type"`
	State       string    `json:"state"`
	Labels      []string  `json:"labels"`
	Comments    []Comment `json:"comments"`
	CreatedAt   time.Time `json:"created_at"`

	// Tracker is nil for an issue that is not mirrored.
	Tracker *TrackerLink `json:"tracker"`
	// TrackerState is the last open/closed the chain saw on the tracker; ""
	// when it never saw one. TrackerStatus is the tracker's own status name.
	TrackerState  string `json:"tracker_state"`
	TrackerStatus string `json:"tracker_status"`
	// DuplicateTrackerIDs lists the tracker issues named by link_tracker ops
	// that lost to an earlier link.
	DuplicateTrackerIDs []string `json:"-"`

	// Warnings lists the commits Load skipped as malformed.
	Warnings []string `json:"-"`
}

// ShortID returns the first 7 characters of the ID.
func (r *Record) ShortID() string {
	if len(r.ID) <= shortIDLen {
		return r.ID
	}

	return r.ID[:shortIDLen]
}

// DisplayID is the ID shown to users and used in branch names: the tracker's
// number for an issue born in the tracker, the short hash otherwise.
func (r *Record) DisplayID() string {
	if r.Tracker != nil {
		return r.Tracker.ID
	}

	return r.ShortID()
}

// Fold computes the Record of issue id from its ops. The order of ops in the
// slice does not matter: they are linearized from their Parents.
func Fold(id string, ops []Op) Record {
	rec := Record{ID: id, State: StateOpen, Labels: []string{}, Comments: []Comment{}}
	labels := make(map[string]bool)

	ordered := chain.Order(ops, func(op *Op) chain.Node {
		return chain.Node{ID: op.ID, Parents: op.Parents, At: op.At}
	})
	for i := range ordered {
		op := &ordered[i]
		switch op.Type {
		case OpCreate:
			rec.Title, rec.Description, rec.BranchType = op.Title, op.Description, op.BranchType
			if op.ID == id {
				rec.CreatedAt = chain.ParseAt(op.At)
				if op.TrackerID != "" {
					rec.Tracker = &TrackerLink{Type: op.TrackerType, Project: op.Project, ID: op.TrackerID, Born: true}
				}
			}
		case OpSetTitle:
			rec.Title = op.Value
		case OpSetDescription:
			rec.Description = op.Value
		case OpSetState:
			if op.Value == StateOpen || op.Value == StateClosed {
				rec.State = op.Value
			}
		case OpAddLabel:
			labels[op.Value] = true
		case OpRemoveLabel:
			delete(labels, op.Value)
		case OpAddComment:
			rec.Comments = append(rec.Comments, Comment{ID: op.ID, Author: op.Author, At: chain.ParseAt(op.At), Body: op.Body})
		case OpLinkTracker:
			switch {
			case op.TrackerID == "":
			case rec.Tracker == nil:
				rec.Tracker = &TrackerLink{Type: op.TrackerType, Project: op.Project, ID: op.TrackerID}
			case rec.Tracker.ID != op.TrackerID && !slices.Contains(rec.DuplicateTrackerIDs, op.TrackerID):
				rec.DuplicateTrackerIDs = append(rec.DuplicateTrackerIDs, op.TrackerID)
			}
		case OpTrackerState:
			if op.Value == StateOpen || op.Value == StateClosed {
				rec.TrackerState, rec.TrackerStatus = op.Value, op.Status
			}
		}
	}

	for l := range labels {
		rec.Labels = append(rec.Labels, l)
	}
	slices.Sort(rec.Labels)

	return rec
}
