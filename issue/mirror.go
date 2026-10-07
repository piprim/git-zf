package issue

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tracker"
)

// importRootTemplate is the op.json of an imported issue's root commit: at,
// tracker type, near slug, tracker number. FROZEN: the ID of every imported
// issue is the hash of a commit holding exactly these bytes, so that two
// clones importing the same tracker issue get the same chain. Changing one
// character makes every clone import its issues a second time.
const importRootTemplate = `{"v":1,"type":"create","at":"%s","tracker_type":"%s","project":"%s","tracker_id":"%s"}`

// plainTokenRe is what the template accepts unescaped.
var plainTokenRe = regexp.MustCompile(`^[0-9A-Za-z_-]+$`)

// Mirror ties the issues of this repository to one tracker project.
type Mirror struct {
	Tracker tracker.Tracker
	Type    string // tracker type, as configured
	Project string // near slug
}

// MirrorResult summarizes one Reconcile.
type MirrorResult struct {
	Imported, Exported int
	Pulled, Pushed     int      // state changes: tracker → repo, repo → tracker
	Warnings           []string // one line per issue that could not be handled
}

type stateMove int

const (
	moveNone   stateMove = iota // no op written
	moveNoted                   // only the tracker state seen was recorded
	movePulled                  // the tracker moved, the repo followed
	movePushed                  // the repo moved, the tracker followed
)

// owns reports whether rec is mirrored with this tracker project.
func (m *Mirror) owns(rec *Record) bool {
	return rec.Tracker != nil && rec.Tracker.Type == m.Type && rec.Tracker.Project == m.Project
}

// Reconcile mirrors the issues with the tracker project: it imports the
// tracker's open issues that have no record, creates a tracker issue for each
// open record that has none, syncs open/closed both ways, and pushes the
// records it changed in one push. It reads every issue in three git processes
// and calls the tracker once for the listing, plus once per issue that closed
// there since the last run. The caller fetches the issue refs first. A failure to
// list the tracker ends the run before anything is written; a failure on one
// issue is a line in MirrorResult.Warnings. A nil Mirror is a no-op.
func (m *Mirror) Reconcile(ctx context.Context, c *git.Client) (MirrorResult, error) {
	var res MirrorResult
	if m == nil {
		return res, nil
	}

	listed, err := m.Tracker.ListProjectIssues(ctx)
	if err != nil {
		return res, fmt.Errorf("list tracker issues: %w", err)
	}

	records, warnings, err := List(ctx, c)
	if err != nil {
		return res, err
	}
	res.Warnings = warnings

	warn := func(format string, args ...any) {
		res.Warnings = append(res.Warnings, "WARN: "+fmt.Sprintf(format, args...))
	}

	open := make(map[string]*tracker.Issue, len(listed))
	for i := range listed {
		open[listed[i].ID] = &listed[i]
	}

	linked := make(map[string]bool, len(records)) // tracker numbers a record links to
	bare := make(map[string]*Record)              // tracker number → born record without a title
	duplicateOf := make(map[string]string)        // losing tracker number → its record
	for i := range records {
		rec := &records[i]
		if !m.owns(rec) {
			continue
		}
		linked[rec.Tracker.ID] = true
		if rec.Tracker.Born && rec.Title == "" {
			bare[rec.Tracker.ID] = rec
		}
		for _, number := range rec.DuplicateTrackerIDs {
			duplicateOf[number] = rec.DisplayID()
		}
	}

	var changed []string
	healed := make(map[string]bool) // records whose import this run completed

	for i := range listed {
		iss := &listed[i]
		if id, named := healImport(ctx, c, bare, iss, warn); named {
			if id != "" {
				res.Imported++
				changed = append(changed, id)
				healed[id] = true
			}

			continue
		}
		if linked[iss.ID] {
			continue
		}
		linked[iss.ID] = true // a listing may name an issue twice

		if owner, dup := duplicateOf[iss.ID]; dup {
			warn("tracker issue %s duplicates the one linked to issue %s: close it in the tracker", iss.ID, owner)

			continue
		}

		id, err := m.importIssue(ctx, c, iss)
		if err != nil {
			warn("import tracker issue %s: %v", iss.ID, err)

			continue
		}
		res.Imported++
		changed = append(changed, id)
	}

	for i := range records {
		rec := &records[i]

		switch {
		case rec.Tracker == nil && rec.State == StateOpen:
			if err := m.export(ctx, c, rec); err != nil {
				warn("export issue %s: %v", rec.DisplayID(), err)

				continue
			}
			res.Exported++
			changed = append(changed, rec.ID)
		case healed[rec.ID]: // rec is the bare record read before the heal: already in sync
		case m.owns(rec):
			move, err := m.syncState(ctx, c, rec, open[rec.Tracker.ID])
			if err != nil {
				warn("sync issue %s: %v", rec.DisplayID(), err)

				continue
			}

			switch move {
			case movePulled:
				res.Pulled++
			case movePushed:
				res.Pushed++
			case moveNone, moveNoted:
			}
			if move != moveNone {
				changed = append(changed, rec.ID)
			}
		}
	}

	// One push for every record touched; one line, not one per issue, when
	// the remote is off.
	if err := PushAll(ctx, c, changed); err != nil {
		warn("issues saved locally but not pushed (run `git zf issue sync` later): %v", err)
	}

	return res, nil
}

// importIssue writes the chain of a tracker issue that has no record and
// returns its ID.
func (m *Mirror) importIssue(ctx context.Context, c *git.Client, iss *tracker.Issue) (string, error) {
	if !plainTokenRe.MatchString(iss.ID) || !plainTokenRe.MatchString(m.Type) || !plainTokenRe.MatchString(m.Project) {
		return "", fmt.Errorf("unsupported characters in %q, %q or %q", iss.ID, m.Type, m.Project)
	}
	if iss.CreatedAt.IsZero() {
		return "", errors.New("the tracker reports no creation date")
	}

	at := iss.CreatedAt.UTC().Truncate(time.Second)
	payload := fmt.Sprintf(importRootTemplate, at.Format(time.RFC3339), m.Type, m.Project, iss.ID)

	id, err := c.WriteFixedChainRoot(ctx, []byte(payload), OpCreate, at)
	if err != nil {
		return "", fmt.Errorf("write root: %w", err)
	}

	tip, err := c.ChainTip(ctx, git.IssueRefs, id)
	if err != nil {
		return "", fmt.Errorf("read ref: %w", err)
	}
	if tip != "" {
		return id, nil // the chain is already here
	}

	if err := c.PublishChainRoot(ctx, git.IssueRefs, id, id); err != nil {
		return "", fmt.Errorf("publish: %w", err)
	}

	return id, appendAll(ctx, c, id, importOps(iss)...)
}

// importOps are the ops that follow the root of an imported issue.
func importOps(iss *tracker.Issue) []*Op {
	ops := []*Op{{Type: OpSetTitle, Value: iss.Subject}}
	if iss.Description != "" {
		ops = append(ops, &Op{Type: OpSetDescription, Value: iss.Description})
	}

	return append(ops, &Op{Type: OpTrackerState, Value: StateOpen, Status: iss.Status})
}

// healImport redoes the ops of an import that stopped after the root was
// published (a failed signature, a crash): the record has no title yet. It
// reports whether iss named such a record, and the record's ID when the ops
// were written.
func healImport(
	ctx context.Context, c *git.Client, bare map[string]*Record, iss *tracker.Issue, warn func(string, ...any),
) (id string, named bool) {
	rec := bare[iss.ID]
	if rec == nil || iss.Subject == "" {
		return "", false
	}
	delete(bare, iss.ID)

	if err := appendAll(ctx, c, rec.ID, importOps(iss)...); err != nil {
		warn("import tracker issue %s: %v", iss.ID, err)

		return "", true
	}

	return rec.ID, true
}

// export creates the tracker issue of a record that has none and links them.
func (m *Mirror) export(ctx context.Context, c *git.Client, rec *Record) error {
	created, err := m.Tracker.CreateIssue(ctx, rec.Title, rec.Description)
	if err != nil {
		return fmt.Errorf("create tracker issue: %w", err)
	}

	return appendAll(ctx, c, rec.ID,
		&Op{Type: OpLinkTracker, TrackerType: m.Type, Project: m.Project, TrackerID: created.ID},
		&Op{Type: OpTrackerState, Value: StateOpen, Status: created.Status})
}

// syncState compares three states of a linked record: l, the tracker state its
// chain last recorded (open when it recorded none); r, the repo state; t, the
// tracker state now. The side that differs from l moved, and the other one
// follows. When both differ from l they are equal: there is no conflict.
// listed is the issue in the open listing, nil when it is not there.
func (m *Mirror) syncState(
	ctx context.Context, c *git.Client, rec *Record, listed *tracker.Issue,
) (stateMove, error) {
	l, r := cmp.Or(rec.TrackerState, StateOpen), rec.State
	t, status := StateOpen, rec.TrackerStatus

	switch {
	case listed != nil:
		status = listed.Status
	case r == StateClosed && l == StateClosed:
		// Closed on both sides and still not listed: no need to ask.
		t = StateClosed
	default:
		closed, err := m.Tracker.IsIssueClosed(ctx, rec.Tracker.ID)
		if err != nil {
			return moveNone, fmt.Errorf("read tracker issue %s: %w", rec.Tracker.ID, err)
		}
		if closed {
			t, status = StateClosed, StateClosed
		}
	}

	seen := &Op{Type: OpTrackerState, Value: t, Status: status}

	switch {
	case t != l && r != t:
		return movePulled, appendAll(ctx, c, rec.ID, &Op{Type: OpSetState, Value: t}, seen)
	case t != l:
		return moveNoted, appendAll(ctx, c, rec.ID, seen)
	case r != t:
		if err := m.Tracker.SetIssueOpen(ctx, rec.Tracker.ID, r == StateOpen); err != nil {
			return moveNone, fmt.Errorf("set tracker issue %s %s: %w", rec.Tracker.ID, r, err)
		}
		// The status name a reopened issue got is read from the next listing.
		seen.Value, seen.Status = r, ""
		if r == StateClosed {
			seen.Status = StateClosed
		}

		return movePushed, appendAll(ctx, c, rec.ID, seen)
	case rec.TrackerState == "" || status != rec.TrackerStatus:
		return moveNoted, appendAll(ctx, c, rec.ID, seen)
	}

	return moveNone, nil
}

// appendAll appends ops to issue id, in order.
func appendAll(ctx context.Context, c *git.Client, id string, ops ...*Op) error {
	for _, op := range ops {
		if err := Append(ctx, c, id, op); err != nil {
			return err
		}
	}

	return nil
}
