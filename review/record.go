// Package review holds the review of an issue as stored in the repository: a
// chain of op commits under refs/zf/reviews/<slug>, folded into a State.
package review

import (
	"encoding/json"
	"time"

	"github.com/piprim/git-zf/internal/chain"
	"github.com/piprim/git-zf/internal/text"
)

// OpVersion is the op.json schema version this binary writes and understands.
const OpVersion = 1

// Op types. Fold skips any type it does not know, so an older binary tolerates
// ops written by a newer one.
const (
	OpRequest = "request"
	OpStart   = "start"
	OpApprove = "approve"
	OpReject  = "reject"
	OpClose   = "close"
	OpMerge   = "merge"
)

// Review statuses.
const (
	StatusInReview         = "in_review"
	StatusApproved         = "approved"
	StatusChangesRequested = "changes_requested"
)

// Op is one action on a review: the content of op.json in one commit of the
// chain at refs/zf/reviews/<slug>. ID and Parents come from the commit itself
// and are not part of the JSON.
type Op struct {
	V      int    `json:"v"`
	Type   string `json:"type"`
	At     string `json:"at"` // RFC 3339, UTC
	Author string `json:"author,omitempty"`

	FeatureSHA  string `json:"feature_sha,omitempty"`  // request
	ApprovedSHA string `json:"approved_sha,omitempty"` // approve
	HasCommits  bool   `json:"has_commits,omitempty"`  // approve, reject
	Comment     string `json:"comment,omitempty"`      // reject
	Round       int    `json:"round,omitempty"`        // start, approve, reject: the round the op was written for

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
	op.Author, op.Comment = text.Line(op.Author), text.Clean(op.Comment)

	return op, true
}

// Approval is one approve op of the current round.
type Approval struct {
	Commit      string // the approve op's commit ID: what a signature covers
	Author      string
	ApprovedSHA string // the commit the reviewer approved
	HasCommits  bool
}

// RoundState is one round of a review: from its request to the decision.
type RoundState struct {
	Round      int
	Status     string
	Reviewer   string
	HasCommits bool
	OpenedAt   time.Time // at of the request op
	ResolvedAt time.Time // at of the last approve or reject op; zero while in review
}

// State is the current state of a review: the fold of its op chain.
type State struct {
	Slug       string
	Status     string // "", in_review, approved, changes_requested
	Round      int
	FeatureSHA string
	Reviewer   string
	Comment    string       // reject comments of the round, joined by a blank line
	HasCommits bool         // OR over the round's approve and reject ops
	Approvals  []Approval   // approvals of the current round
	Rounds     []RoundState // every round, oldest first; the last one is the current round
	Closed     bool
	UpdatedAt  time.Time // at of the last applied op

	// Warnings lists the commits Load skipped as malformed.
	Warnings []string
}

// Fold computes the State of slug's review from its ops. The order of ops in
// the slice does not matter: they are linearized from their Parents. A start,
// approve or reject written for another round than the current one is ignored.
func Fold(slug string, ops []Op) State {
	st := State{Slug: slug}

	ordered := chain.Order(ops, func(op *Op) chain.Node {
		return chain.Node{ID: op.ID, Parents: op.Parents, At: op.At}
	})
	for i := range ordered {
		op := &ordered[i]
		if !apply(&st, op) {
			continue
		}
		st.UpdatedAt = chain.ParseAt(op.At)

		if op.Type == OpRequest {
			st.Rounds = append(st.Rounds, RoundState{Round: st.Round, OpenedAt: st.UpdatedAt})
		}
		if len(st.Rounds) == 0 {
			continue // a close before any request
		}
		r := &st.Rounds[len(st.Rounds)-1]
		r.Status, r.Reviewer, r.HasCommits = st.Status, st.Reviewer, st.HasCommits
		if op.Type == OpApprove || op.Type == OpReject {
			r.ResolvedAt = st.UpdatedAt
		}
	}

	return st
}

// apply applies op to st and reports whether it changed anything. An op that
// does not fit the current status is ignored: that is how concurrent actions
// resolve the same way on every clone. A start, approve or reject whose Round
// is set and differs from the current round was written for another round (a
// reviewer with a stale view) and is ignored; Round 0 means unbound.
func apply(st *State, op *Op) bool {
	switch op.Type {
	case OpStart, OpApprove, OpReject:
		if op.Round != 0 && op.Round != st.Round {
			return false
		}
	}

	switch op.Type {
	case OpRequest:
		// A second request while in review is a duplicate (two clones
		// requested concurrently), not a new round.
		if st.Status == StatusInReview {
			return false
		}
		st.Round++
		st.Status, st.FeatureSHA = StatusInReview, op.FeatureSHA
		st.Reviewer, st.Comment, st.HasCommits, st.Approvals, st.Closed = "", "", false, nil, false
	case OpStart:
		if st.Status != StatusInReview || st.Reviewer != "" {
			return false
		}
		st.Reviewer = op.Author
	case OpApprove:
		// Every approval of the round is kept: the one that covers the final
		// tip may be the second one.
		if st.Status != StatusInReview && st.Status != StatusApproved {
			return false
		}
		st.Status = StatusApproved
		st.Approvals = append(st.Approvals, Approval{
			Commit: op.ID, Author: op.Author, ApprovedSHA: op.ApprovedSHA, HasCommits: op.HasCommits,
		})
		st.HasCommits = st.HasCommits || op.HasCommits
	case OpReject:
		// A reject wins over a concurrent approve whichever sorts first: it
		// applies on top of approved, and an approve does not apply on top of
		// changes_requested.
		if st.Status == "" {
			return false
		}
		st.Status, st.Approvals = StatusChangesRequested, nil
		if op.Comment != "" {
			if st.Comment != "" {
				st.Comment += "\n\n"
			}
			st.Comment += op.Comment
		}
		st.HasCommits = st.HasCommits || op.HasCommits
	case OpClose:
		st.Closed = true
	default:
		return false
	}

	return true
}
