package issue

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"
)

// OpVersion is the op.json schema version this binary writes and understands.
const OpVersion = 1

// Op types. Fold skips any type it does not know, so an older binary tolerates
// ops written by a newer one.
const (
	OpCreate      = "create"
	OpSetState    = "set_state"
	OpAddLabel    = "add_label"
	OpRemoveLabel = "remove_label"
	OpAddComment  = "add_comment"
	OpMerge       = "merge"
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
	Value       string `json:"value,omitempty"`       // set_state, add_label, remove_label
	Body        string `json:"body,omitempty"`        // add_comment

	ID      string   `json:"-"`
	Parents []string `json:"-"`
}

// DecodeOp builds the Op of commit id from its op.json payload. ok is false
// when the payload is missing, is not valid JSON or has an unknown version; the
// returned Op then has an empty Type, so Fold ignores it while its ID and
// Parents still keep the chain connected.
func DecodeOp(id string, parents []string, payload []byte) (op Op, ok bool) {
	if err := json.Unmarshal(payload, &op); err != nil || op.V != OpVersion {
		return Op{ID: id, Parents: parents}, false
	}

	op.ID, op.Parents = id, parents

	return op, true
}

// Comment is one add_comment op.
type Comment struct {
	ID     string    `json:"id"`
	Author string    `json:"author"`
	At     time.Time `json:"at"`
	Body   string    `json:"body"`
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

// DisplayID is the ID shown to users and used in branch names.
func (r *Record) DisplayID() string {
	return r.ShortID()
}

// Fold computes the Record of issue id from its ops. The order of ops in the
// slice does not matter: they are linearized from their Parents.
func Fold(id string, ops []Op) Record {
	rec := Record{ID: id, State: StateOpen, Labels: []string{}, Comments: []Comment{}}
	labels := make(map[string]bool)

	ordered := linearize(ops)
	for i := range ordered {
		op := &ordered[i]
		switch op.Type {
		case OpCreate:
			rec.Title, rec.Description, rec.BranchType = op.Title, op.Description, op.BranchType
			if op.ID == id {
				rec.CreatedAt = parseAt(op.At)
			}
		case OpSetState:
			if op.Value == StateOpen || op.Value == StateClosed {
				rec.State = op.Value
			}
		case OpAddLabel:
			labels[op.Value] = true
		case OpRemoveLabel:
			delete(labels, op.Value)
		case OpAddComment:
			rec.Comments = append(rec.Comments, Comment{ID: op.ID, Author: op.Author, At: parseAt(op.At), Body: op.Body})
		}
	}

	for l := range labels {
		rec.Labels = append(rec.Labels, l)
	}
	slices.Sort(rec.Labels)

	return rec
}

func parseAt(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}

	return t.UTC()
}

// linearize orders ops so that every op comes after all of its parents. Ops
// with no ordering between them (written concurrently on two clones) are
// ordered by At, then by ID, so every clone computes the same sequence.
func linearize(ops []Op) []Op {
	byID := make(map[string]*Op, len(ops))
	for i := range ops {
		byID[ops[i].ID] = &ops[i]
	}

	pending := make(map[string]int, len(ops))
	children := make(map[string][]string, len(ops))
	for i := range ops {
		for _, p := range ops[i].Parents {
			if _, known := byID[p]; !known {
				continue
			}
			pending[ops[i].ID]++
			children[p] = append(children[p], ops[i].ID)
		}
	}

	var ready []string
	for i := range ops {
		if pending[ops[i].ID] == 0 {
			ready = append(ready, ops[i].ID)
		}
	}

	out := make([]Op, 0, len(ops))
	for len(ready) > 0 {
		// ponytail: re-sorts the ready set at every step, O(n² log n) worst
		// case; switch to container/heap if a chain reaches thousands of ops.
		slices.SortFunc(ready, func(a, b string) int {
			if c := parseAt(byID[a].At).Compare(parseAt(byID[b].At)); c != 0 {
				return c
			}

			return cmp.Compare(a, b)
		})

		next := ready[0]
		ready = ready[1:]
		out = append(out, *byID[next])

		for _, child := range children[next] {
			pending[child]--
			if pending[child] == 0 {
				ready = append(ready, child)
			}
		}
	}

	return out
}
