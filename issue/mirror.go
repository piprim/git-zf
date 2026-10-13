package issue

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tracker"
)

// importRootTemplate is the op.json of an imported issue's root commit: at,
// tracker type, project name, tracker number. FROZEN: the ID of every imported
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
	Project string // the configured project name
	// labels memoizes Labels: the tracker's labels rarely change and the
	// issue table asks on every `l`. Reconcile forgets them.
	labels []string
}

// Labels lists the tracker's labels, asking it once per process and after
// each Reconcile.
func (m *Mirror) Labels(ctx context.Context) ([]string, error) {
	if m.labels == nil {
		names, err := m.Tracker.ListLabels(ctx)
		if err != nil {
			return nil, fmt.Errorf("list tracker labels: %w", err)
		}
		m.labels = append([]string{}, names...)
	}

	return slices.Clone(m.labels), nil
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

	listed, err := m.Tracker.ListIssues(ctx)
	if err != nil {
		return res, fmt.Errorf("list tracker issues: %w", err)
	}
	m.labels = nil

	// The fold warnings of List are not reported here: the command prints
	// them from its own read, and would otherwise print them twice.
	records, _, err := List(ctx, c)
	if err != nil {
		return res, err
	}

	open := make(map[string]*tracker.Issue, len(listed))
	for i := range listed {
		open[listed[i].ID] = &listed[i]
	}

	linked, bare, duplicateOf := m.index(records)

	var changed []string
	healed := make(map[string]bool) // records a heal touched this run

	for i := range listed {
		iss := &listed[i]
		if id, ok := m.healImport(ctx, c, bare, iss, res.warnf); id != "" {
			healed[id] = true
			if ok {
				res.Imported++
				changed = append(changed, id)
			}

			continue
		}
		if linked[iss.ID] {
			continue
		}
		linked[iss.ID] = true // a listing may name an issue twice

		if owner, dup := duplicateOf[iss.ID]; dup {
			res.warnf("tracker issue %s duplicates the one linked to issue %s: close it in the tracker", iss.ID, owner)

			continue
		}

		id, err := m.importIssue(ctx, c, iss, res.warnf)
		if err != nil {
			res.warnf("import tracker issue %s: %v", iss.ID, err)

			continue
		}
		res.Imported++
		changed = append(changed, id)
	}

	// The records a heal touched were read before it: in sync, or still bare.
	records = slices.DeleteFunc(records, func(rec Record) bool { return healed[rec.ID] })
	changed = append(changed, m.syncRecords(ctx, c, records, open, &res)...)

	// One push for every record touched; one line, not one per issue, when
	// the remote is off.
	if err := PushAll(ctx, c, changed); err != nil {
		res.warnf("issues saved locally but not pushed (run `git zf issue sync` later): %v", err)
	}

	return res, nil
}

// index reads the records the mirror owns: the tracker numbers they link to,
// the born records without a title (an import that stopped) by number, and
// the record each losing duplicate number belongs to.
func (m *Mirror) index(records []Record) (linked map[string]bool, bare map[string]*Record, duplicateOf map[string]string) {
	linked = make(map[string]bool, len(records))
	bare = make(map[string]*Record)
	duplicateOf = make(map[string]string)
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

	return linked, bare, duplicateOf
}

// warnf adds one warning line.
func (r *MirrorResult) warnf(format string, args ...any) {
	r.Warnings = append(r.Warnings, "WARN: "+fmt.Sprintf(format, args...))
}

// contentMove sums what the content steps did to one record.
type contentMove struct {
	pulled, pushed bool
}

// syncRecords exports the open records without a tracker issue and runs the
// four steps on the linked ones. It returns the IDs of the records it changed.
func (m *Mirror) syncRecords(
	ctx context.Context, c *git.Client, records []Record, open map[string]*tracker.Issue, res *MirrorResult,
) []string {
	var changed []string

	for i := range records {
		rec := &records[i]
		var listed *tracker.Issue

		switch {
		case rec.Tracker == nil && rec.State == StateOpen:
			created, err := m.export(ctx, c, rec)
			if err != nil {
				res.warnf("export issue %s: %v", rec.DisplayID(), err)

				continue
			}
			res.Exported++
			changed = append(changed, rec.ID)
			listed = &created
		case m.owns(rec):
			listed = open[rec.Tracker.ID]
		default:
			continue
		}

		pulled, pushed, moved, err := m.syncRecord(ctx, c, rec, listed, res)
		if err != nil {
			res.warnf("sync issue %s: %v", rec.DisplayID(), err)
		}
		if pulled {
			res.Pulled++
		}
		if pushed {
			res.Pushed++
		}
		if moved && !slices.Contains(changed, rec.ID) {
			changed = append(changed, rec.ID)
		}
	}

	return changed
}

// importIssue writes the chain of a tracker issue that has no record and
// returns its ID.
func (m *Mirror) importIssue(ctx context.Context, c *git.Client, iss *tracker.Issue, warn func(string, ...any)) (string, error) {
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

	return id, m.importContent(ctx, c, id, iss, warn)
}

// importContent writes the ops that follow the root of an imported issue and
// pulls its comments. A failure to list the comments is not an error: the
// snapshot written here has no updated time, so the next run reads them.
func (m *Mirror) importContent(ctx context.Context, c *git.Client, id string, iss *tracker.Issue, warn func(string, ...any)) error {
	ops := importOps(iss)
	snapshot := ops[len(ops)-1]

	comments, err := m.Tracker.ListComments(ctx, iss.ID)
	if err != nil {
		warn("import comments of tracker issue %s: %v (they come with the next run)", iss.ID, err)
		snapshot.Updated = ""
	}
	if err := appendAll(ctx, c, id, ops[:len(ops)-1]...); err != nil {
		return err
	}
	for _, tc := range comments {
		if tc.Body == "" {
			continue
		}
		op := &Op{Type: OpAddComment, Body: tc.Body, TrackerCommentID: tc.ID, TrackerAuthor: tc.Author,
			TrackerAt: tc.CreatedAt.UTC().Format(time.RFC3339Nano)}
		if err := appendAll(ctx, c, id, op); err != nil {
			return err
		}
	}

	return appendAll(ctx, c, id, snapshot)
}

// importOps are the ops that follow the root of an imported issue.
func importOps(iss *tracker.Issue) []*Op {
	ops := []*Op{{Type: OpSetTitle, Value: iss.Subject}}
	if iss.Description != "" {
		ops = append(ops, &Op{Type: OpSetDescription, Value: iss.Description})
	}
	for _, l := range iss.Labels {
		ops = append(ops, &Op{Type: OpAddLabel, Value: l})
	}
	snap := snapshotOf(iss)

	return append(ops, &Op{Type: OpTrackerState, Value: StateOpen, Status: iss.Status}, snapshotOp(&snap))
}

// healImport redoes the ops of an import that stopped after the root was
// published (a failed signature, a crash): the record has no title yet. It
// returns the ID of such a record ("" when iss names none) and whether the
// ops were written.
func (m *Mirror) healImport(
	ctx context.Context, c *git.Client, bare map[string]*Record, iss *tracker.Issue, warn func(string, ...any),
) (id string, ok bool) {
	rec := bare[iss.ID]
	if rec == nil || iss.Subject == "" {
		return "", false
	}
	delete(bare, iss.ID)

	if err := m.importContent(ctx, c, rec.ID, iss, warn); err != nil {
		warn("import tracker issue %s: %v", iss.ID, err)

		return rec.ID, false
	}

	return rec.ID, true
}

// export creates the tracker issue of a record that has none, links them and
// returns the created issue, so that the caller syncs the record against it
// at once.
func (m *Mirror) export(ctx context.Context, c *git.Client, rec *Record) (tracker.Issue, error) {
	created, err := m.Tracker.CreateIssue(ctx, rec.Title, rec.Description)
	if err != nil {
		return tracker.Issue{}, fmt.Errorf("create tracker issue: %w", err)
	}

	// The record takes the labels the tracker gave the new issue (Redmine's
	// default status), as an import does.
	snap := snapshotOf(&created)
	ops := []*Op{
		{Type: OpLinkTracker, TrackerType: m.Type, Project: m.Project, TrackerID: created.ID},
		{Type: OpTrackerState, Value: StateOpen, Status: created.Status},
	}
	for _, l := range created.Labels {
		ops = append(ops, &Op{Type: OpAddLabel, Value: l})
	}
	if err := appendAll(ctx, c, rec.ID, append(ops, snapshotOp(&snap))...); err != nil {
		// The next run exports again: the issue named here is the orphan.
		return tracker.Issue{}, fmt.Errorf("link tracker issue %s (created, now orphaned): %w", created.ID, err)
	}
	rec.Tracker = &TrackerLink{Type: m.Type, Project: m.Project, ID: created.ID}
	rec.Labels = slices.Compact(slices.Sorted(slices.Values(slices.Concat(rec.Labels, created.Labels))))
	rec.TrackerState, rec.TrackerStatus, rec.TrackerSeen = StateOpen, created.Status, &snap

	return created, nil
}

// SyncOne runs the four steps on one record and pushes it: what `edit`,
// `label` and `comment` call after their push, so that the tracker gets the
// change at once. The issue is read with one call, no listing. A nil Mirror,
// or a record the mirror does not own, is a no-op.
func (m *Mirror) SyncOne(ctx context.Context, c *git.Client, id string) (MirrorResult, error) {
	var res MirrorResult
	if m == nil {
		return res, nil
	}

	rec, err := Load(ctx, c, id)
	if err != nil {
		return res, err
	}
	if !m.owns(&rec) {
		return res, nil
	}

	// Read here, not left to syncRecord: a record closed on both sides still
	// gets its comments posted.
	t, err := m.reread(ctx, &rec)
	if err != nil {
		return res, err
	}
	pulled, pushed, changed, err := m.syncRecord(ctx, c, &rec, t, &res)
	if pulled {
		res.Pulled++
	}
	if pushed {
		res.Pushed++
	}
	if changed {
		if perr := Push(ctx, c, id); perr != nil {
			res.warnf("issue saved locally but not pushed (run `git zf issue sync` later): %v", perr)
		}
	}

	return res, err
}

// syncRecord runs the four steps on one linked record: state, content, state
// again on the issue as re-read after a push, then the snapshot. listed is
// the issue in the open listing, nil when it is not there. changed reports
// whether an op was written, even when err is set.
func (m *Mirror) syncRecord(
	ctx context.Context, c *git.Client, rec *Record, listed *tracker.Issue, res *MirrorResult,
) (pulled, pushed, changed bool, err error) {
	t, err := m.trackerIssue(ctx, rec, listed)
	if err != nil || t == nil {
		return false, false, false, err
	}

	move, err := m.syncState(ctx, c, rec, t)
	if err != nil {
		return false, false, false, err
	}
	pulled, pushed, changed = move == movePulled, move == movePushed, move != moveNone
	if move == movePushed {
		if t, err = m.reread(ctx, rec); err != nil {
			return pulled, pushed, changed, err
		}
	}

	cm, err := m.syncContent(ctx, c, rec, t, res)
	pulled, pushed, changed = pulled || cm.pulled, pushed || cm.pushed, changed || cm.pulled || cm.pushed
	if err != nil {
		return pulled, pushed, changed, err
	}
	if cm.pushed {
		if t, err = m.reread(ctx, rec); err != nil {
			return pulled, pushed, changed, err
		}
		adopted, err := adopt(ctx, c, rec, t)
		changed = changed || adopted
		if err != nil {
			return pulled, pushed, changed, err
		}
		move, err = m.syncState(ctx, c, rec, t)
		if err != nil {
			return pulled, pushed, changed, err
		}
		pulled, changed = pulled || move == movePulled, changed || move != moveNone
	}

	snap := snapshotOf(t)
	if !sameSnapshot(rec.TrackerSeen, &snap) {
		if err := appendAll(ctx, c, rec.ID, snapshotOp(&snap)); err != nil {
			return pulled, pushed, changed, err
		}
		rec.TrackerSeen, changed = &snap, true
	}

	return pulled, pushed, changed, nil
}

// adopt runs the pull half of the rule on t as re-read after a push, the
// baseline being what was sent (the record): a value the tracker respelled
// (a label's case, a trimmed title) is taken back, so the next run is settled.
func adopt(ctx context.Context, c *git.Client, rec *Record, t *tracker.Issue) (bool, error) {
	title, pullTitle, _, _ := resolveField(rec.Title, rec.Title, t.Subject)
	desc, pullDesc, _, _ := resolveField(rec.Description, rec.Description, t.Description)
	labels, ops, _ := resolveLabels(rec.Labels, rec.Labels, t.Labels)
	if pullTitle {
		ops = append(ops, &Op{Type: OpSetTitle, Value: title})
	}
	if pullDesc {
		ops = append(ops, &Op{Type: OpSetDescription, Value: desc})
	}
	if len(ops) == 0 {
		return false, nil
	}
	if err := appendAll(ctx, c, rec.ID, ops...); err != nil {
		return false, err
	}
	rec.Title, rec.Description, rec.Labels = title, desc, labels

	return true, nil
}

// trackerIssue is the issue as the tracker shows it now: listed when it is
// in the open listing, read with one call otherwise. nil, nil for a record
// closed on both sides, not listed and with nothing to push: nothing to
// compare.
func (m *Mirror) trackerIssue(ctx context.Context, rec *Record, listed *tracker.Issue) (*tracker.Issue, error) {
	if listed != nil {
		return listed, nil
	}
	if rec.State == StateClosed && cmp.Or(rec.TrackerState, StateOpen) == StateClosed && recordSettled(rec) {
		return nil, nil
	}

	return m.reread(ctx, rec)
}

// reread reads the record's tracker issue.
func (m *Mirror) reread(ctx context.Context, rec *Record) (*tracker.Issue, error) {
	iss, err := m.Tracker.GetIssue(ctx, rec.Tracker.ID)
	if err != nil {
		return nil, fmt.Errorf("read tracker issue %s: %w", rec.Tracker.ID, err)
	}

	return &iss, nil
}

// syncState compares three states of a linked record: l, the tracker state its
// chain last recorded (open when it recorded none); r, the repo state; t, the
// tracker state now. The side that differs from l moved, and the other one
// follows. When both differ from l they are equal: there is no conflict. The
// record is updated in memory with what was appended.
func (m *Mirror) syncState(ctx context.Context, c *git.Client, rec *Record, t *tracker.Issue) (stateMove, error) {
	l, r := cmp.Or(rec.TrackerState, StateOpen), rec.State
	state, status := StateOpen, t.Status
	if t.Closed {
		state = StateClosed
		if status == "" {
			status = StateClosed
		}
	}

	seen := &Op{Type: OpTrackerState, Value: state, Status: status}
	note := func() { rec.TrackerState, rec.TrackerStatus = seen.Value, seen.Status }

	switch {
	case state != l && r != state:
		err := appendAll(ctx, c, rec.ID, &Op{Type: OpSetState, Value: state}, seen)
		if err == nil {
			rec.State = state
			note()
		}

		return movePulled, err
	case state != l:
		err := appendAll(ctx, c, rec.ID, seen)
		if err == nil {
			note()
		}

		return moveNoted, err
	case r != state:
		if err := m.Tracker.SetIssueOpen(ctx, rec.Tracker.ID, r == StateOpen); err != nil {
			return moveNone, fmt.Errorf("set tracker issue %s %s: %w", rec.Tracker.ID, r, err)
		}
		// The status name a reopened issue got is read from the re-read.
		seen.Value, seen.Status = r, ""
		if r == StateClosed {
			seen.Status = StateClosed
		}
		err := appendAll(ctx, c, rec.ID, seen)
		if err == nil {
			note()
		}

		return movePushed, err
	case rec.TrackerState == "" || status != rec.TrackerStatus:
		err := appendAll(ctx, c, rec.ID, seen)
		if err == nil {
			note()
		}

		return moveNoted, err
	}

	return moveNone, nil
}

// syncContent resolves title, description, labels and comments between the
// baseline, the record and t. A push goes to the tracker before any pull op
// is written, so that a failed push leaves the chain as it was and the next
// run redoes the whole step. Comments come last: pulled first, so that a
// comment posted here is never pulled back as a new one.
func (m *Mirror) syncContent(
	ctx context.Context, c *git.Client, rec *Record, t *tracker.Issue, res *MirrorResult,
) (contentMove, error) {
	var cm contentMove
	if contentSettled(rec, t) {
		return cm, nil
	}
	base := baseline(rec)

	title, pullTitle, pushTitle, conflictTitle := resolveField(base.Title, rec.Title, t.Subject)
	desc, pullDesc, pushDesc, conflictDesc := resolveField(base.Description, rec.Description, t.Description)
	if conflictTitle {
		res.warnf("issue %s: title changed on both sides; kept %q, the tracker had %q", rec.DisplayID(), rec.Title, t.Subject)
	}
	if conflictDesc {
		res.warnf("issue %s: description changed on both sides; kept the repo's, the tracker had %q", rec.DisplayID(), t.Description)
	}

	if pushTitle || pushDesc {
		if err := m.Tracker.UpdateIssue(ctx, rec.Tracker.ID, title, desc); err != nil {
			return cm, fmt.Errorf("update tracker issue %s: %w", rec.Tracker.ID, err)
		}
		cm.pushed = true
	}

	labels, pullLabels, pushLabels := resolveLabels(base.Labels, rec.Labels, t.Labels)
	if pushLabels {
		err := m.Tracker.SetLabels(ctx, rec.Tracker.ID, labels)
		switch {
		case errors.Is(err, tracker.ErrLabelRule):
			// The record's labels cannot exist on the tracker: take the
			// tracker's back, once, and say which names are dropped.
			var dropped []string
			for _, name := range rec.Labels {
				if !slices.Contains(t.Labels, name) {
					dropped = append(dropped, name)
				}
			}
			labels, pullLabels, _ = resolveLabels(rec.Labels, rec.Labels, t.Labels)
			res.warnf("issue %s: labels %q refused by the tracker, kept %q: %v", rec.DisplayID(), dropped, labels, err)
		case err != nil:
			return cm, fmt.Errorf("set labels of tracker issue %s: %w", rec.Tracker.ID, err)
		default:
			cm.pushed = true
		}
	}

	var ops []*Op
	if pullTitle {
		ops = append(ops, &Op{Type: OpSetTitle, Value: title})
	}
	if pullDesc {
		ops = append(ops, &Op{Type: OpSetDescription, Value: desc})
	}
	ops = append(ops, pullLabels...)
	if len(ops) > 0 {
		if err := appendAll(ctx, c, rec.ID, ops...); err != nil {
			return cm, err
		}
		rec.Title, rec.Description, cm.pulled = title, desc, true
		rec.Labels = labels
	}

	pulledComments, err := m.pullComments(ctx, c, rec, t)
	cm.pulled = cm.pulled || pulledComments
	if err != nil {
		return cm, err
	}
	pushedComments, err := m.pushComments(ctx, c, rec)
	cm.pushed = cm.pushed || pushedComments
	if err != nil {
		return cm, err
	}

	return cm, nil
}

// pullComments appends the tracker's comments the record lacks, oldest
// first, when the tracker moved since the snapshot. An empty body is skipped.
// A tracker comment with the body of a local comment not yet linked is that
// comment, posted by a run whose link never landed: it is linked, not pulled.
func (m *Mirror) pullComments(ctx context.Context, c *git.Client, rec *Record, t *tracker.Issue) (bool, error) {
	if rec.TrackerSeen != nil && t.UpdatedAt.Equal(rec.TrackerSeen.Updated) {
		return false, nil
	}

	comments, err := m.Tracker.ListComments(ctx, rec.Tracker.ID)
	if err != nil {
		return false, fmt.Errorf("list comments of tracker issue %s: %w", rec.Tracker.ID, err)
	}
	known := make(map[string]bool, len(rec.Comments))
	unlinked := make(map[string]int) // body → index of the first unlinked local comment
	for i, rc := range rec.Comments {
		known[rc.TrackerID] = true
		if _, seen := unlinked[rc.Body]; rc.TrackerID == "" && !seen {
			unlinked[rc.Body] = i
		}
	}

	var ops []*Op
	for _, tc := range comments {
		if tc.Body == "" || known[tc.ID] {
			continue
		}
		if i, ok := unlinked[tc.Body]; ok {
			delete(unlinked, tc.Body)
			ops = append(ops, &Op{Type: OpLinkComment, Comment: rec.Comments[i].ID, TrackerCommentID: tc.ID})
			rec.Comments[i].TrackerID = tc.ID

			continue
		}
		ops = append(ops, &Op{
			Type: OpAddComment, Body: tc.Body, TrackerCommentID: tc.ID, TrackerAuthor: tc.Author,
			TrackerAt: tc.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
		rec.Comments = append(rec.Comments, Comment{Author: tc.Author, At: tc.CreatedAt, Body: tc.Body, TrackerID: tc.ID})
	}
	if len(ops) == 0 {
		return false, nil
	}

	return true, appendAll(ctx, c, rec.ID, ops...)
}

// pushComments posts every record comment the tracker has not got and links
// each one with the ID it was given.
func (m *Mirror) pushComments(ctx context.Context, c *git.Client, rec *Record) (bool, error) {
	pushed := false
	for i := range rec.Comments {
		rc := &rec.Comments[i]
		if rc.TrackerID != "" {
			continue
		}
		posted, err := m.Tracker.AddComment(ctx, rec.Tracker.ID, rc.Body)
		if err != nil {
			return pushed, fmt.Errorf("post comment on tracker issue %s: %w", rec.Tracker.ID, err)
		}
		pushed = true
		if err := appendAll(ctx, c, rec.ID, &Op{Type: OpLinkComment, Comment: rc.ID, TrackerCommentID: posted.ID}); err != nil {
			// The next run posts it again: the comment named here is the orphan.
			return pushed, fmt.Errorf("link comment %s (posted on tracker issue %s as %s, now orphaned): %w",
				rc.ID, rec.Tracker.ID, posted.ID, err)
		}
		rc.TrackerID = posted.ID
	}

	return pushed, nil
}

// resolveLabels applies the rule to every name of l, r and t: the side that
// differs from the baseline moved. It returns the final set, the add/remove
// ops that take the record to it, and whether the tracker must be set.
func resolveLabels(l, r, t []string) (final []string, pull []*Op, push bool) {
	names := slices.Concat(l, r, t)
	slices.Sort(names)
	names = slices.Compact(names)

	for _, name := range names {
		inL, inR, inT := slices.Contains(l, name), slices.Contains(r, name), slices.Contains(t, name)
		switch {
		case inT && !inL && !inR:
			pull = append(pull, &Op{Type: OpAddLabel, Value: name})
			final = append(final, name)
		case !inT && inL && inR:
			pull = append(pull, &Op{Type: OpRemoveLabel, Value: name})
		case inR && !inL && !inT, !inR && inL && inT:
			push = true
			if inR {
				final = append(final, name)
			}
		case inR:
			final = append(final, name)
		}
	}
	if final == nil {
		final = []string{}
	}

	return final, pull, push
}

// resolveField applies the rule of one field: l the baseline, r the record, t
// the tracker. The side that differs from l moved. Both sides moved apart is
// a conflict the record wins.
func resolveField(l, r, t string) (final string, pull, push, conflict bool) {
	switch {
	case t == l && r == l:
		return r, false, false, false
	case t != l && r == l:
		return t, true, false, false
	case r != l && t == l:
		return r, false, true, false
	case r == t:
		return r, false, false, false
	default:
		return r, false, true, true
	}
}

// baseline is the snapshot, or the record itself when the chain has none (a
// link written before snapshots existed): the tracker is then pulled
// wherever it differs.
func baseline(rec *Record) TrackerSnapshot {
	if rec.TrackerSeen != nil {
		return *rec.TrackerSeen
	}

	return TrackerSnapshot{Title: rec.Title, Description: rec.Description, Labels: slices.Clone(rec.Labels)}
}

// contentSettled reports that nothing moved on either side since the
// snapshot: the tracker's updated time is the snapshot's and the record has
// nothing to push.
func contentSettled(rec *Record, t *tracker.Issue) bool {
	s := rec.TrackerSeen

	return s != nil && !s.Updated.IsZero() && t.UpdatedAt.Equal(s.Updated) && recordSettled(rec)
}

// recordSettled reports that the record has nothing to push: it equals its
// snapshot and every comment is on the tracker.
func recordSettled(rec *Record) bool {
	s := rec.TrackerSeen
	if s == nil || rec.Title != s.Title || rec.Description != s.Description || !slices.Equal(rec.Labels, s.Labels) {
		return false
	}
	for _, c := range rec.Comments {
		if c.TrackerID == "" {
			return false
		}
	}

	return true
}

// snapshotOf is the snapshot of t: what the tracker shows.
func snapshotOf(t *tracker.Issue) TrackerSnapshot {
	labels := slices.Clone(t.Labels)
	if labels == nil {
		labels = []string{}
	}
	slices.Sort(labels)

	return TrackerSnapshot{Title: t.Subject, Description: t.Description, Labels: labels, Updated: t.UpdatedAt}
}

func sameSnapshot(a, b *TrackerSnapshot) bool {
	return a != nil && a.Title == b.Title && a.Description == b.Description &&
		slices.Equal(a.Labels, b.Labels) && a.Updated.Equal(b.Updated)
}

// snapshotOp is the tracker_seen op of s.
func snapshotOp(s *TrackerSnapshot) *Op {
	op := &Op{Type: OpTrackerSeen, Title: s.Title, Description: s.Description, Labels: s.Labels}
	if !s.Updated.IsZero() {
		op.Updated = s.Updated.UTC().Format(time.RFC3339Nano)
	}

	return op
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
