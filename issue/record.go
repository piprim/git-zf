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
	OpTrackerSeen    = "tracker_seen"
	OpLinkComment    = "link_comment"
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

	Labels  []string `json:"labels,omitempty"`  // tracker_seen: the tracker's labels
	Updated string   `json:"updated,omitempty"` // tracker_seen: the tracker's updated time, RFC 3339

	TrackerCommentID string `json:"tracker_comment_id,omitempty"` // add_comment (imported), link_comment
	TrackerAuthor    string `json:"tracker_author,omitempty"`     // add_comment (imported): the tracker login
	TrackerAt        string `json:"tracker_at,omitempty"`         // add_comment (imported): the tracker's time, RFC 3339
	Comment          string `json:"comment,omitempty"`            // link_comment: the op ID of the add_comment

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
	op.Updated, op.TrackerCommentID, op.TrackerAuthor, op.TrackerAt, op.Comment =
		text.Line(op.Updated), text.Line(op.TrackerCommentID), text.Line(op.TrackerAuthor), text.Line(op.TrackerAt), text.Line(op.Comment)
	for i := range op.Labels {
		op.Labels[i] = text.Line(op.Labels[i])
	}
	op.Description, op.Body = text.Clean(op.Description), text.Clean(op.Body)
	if op.Type == OpSetDescription {
		op.Value = text.Clean(op.Value)
	} else {
		op.Value = text.Line(op.Value)
	}

	return op, true
}

// Comment is one add_comment op. Author and At are the tracker's when the
// comment was imported from it. TrackerID is the tracker's comment ID, ""
// for a comment the tracker has not got yet.
type Comment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	At        time.Time `json:"at"`
	Body      string    `json:"body"`
	TrackerID string    `json:"tracker_id,omitempty"`
}

// TrackerSnapshot is what the tracker showed the last time the mirror read
// the issue: the baseline the reconcile compares both sides with.
type TrackerSnapshot struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Labels      []string  `json:"labels"`
	Updated     time.Time `json:"updated"`
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
	// TrackerSeen is the last tracker_seen op; nil when the chain has none.
	TrackerSeen *TrackerSnapshot `json:"tracker_seen"`
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
	seenComments := make(map[string]bool)

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
			if op.TrackerCommentID != "" {
				if seenComments[op.TrackerCommentID] {
					continue
				}
				seenComments[op.TrackerCommentID] = true
			}
			c := Comment{ID: op.ID, Author: op.Author, At: chain.ParseAt(op.At), Body: op.Body, TrackerID: op.TrackerCommentID}
			if op.TrackerAuthor != "" {
				c.Author = op.TrackerAuthor
			}
			if op.TrackerAt != "" {
				c.At = chain.ParseAt(op.TrackerAt)
			}
			rec.Comments = append(rec.Comments, c)
		case OpLinkComment:
			i := slices.IndexFunc(rec.Comments, func(c Comment) bool { return c.ID == op.Comment && c.TrackerID == "" })
			switch {
			case i < 0 || op.TrackerCommentID == "":
			case seenComments[op.TrackerCommentID]:
				// Another clone pulled the comment this one posted: keep one.
				rec.Comments = slices.Delete(rec.Comments, i, i+1)
			default:
				rec.Comments[i].TrackerID = op.TrackerCommentID
				seenComments[op.TrackerCommentID] = true
			}
		case OpTrackerSeen:
			seen := &TrackerSnapshot{Title: op.Title, Description: op.Description, Labels: slices.Clone(op.Labels)}
			if seen.Labels == nil {
				seen.Labels = []string{}
			}
			slices.Sort(seen.Labels)
			if op.Updated != "" {
				seen.Updated = chain.ParseAt(op.Updated)
			}
			rec.TrackerSeen = seen
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
