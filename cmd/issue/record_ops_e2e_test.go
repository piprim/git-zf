package issue

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/config"
	issuepkg "github.com/piprim/git-zf/issue"
)

func TestRunComment(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "T", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	comments := func(t *testing.T) []string {
		t.Helper()

		got, err := issuepkg.Load(ctx, rig.client, rec.ID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		bodies := make([]string, len(got.Comments))
		for i, c := range got.Comments {
			bodies[i] = c.Body
		}

		return bodies
	}

	t.Run("--message adds the comment without the form", func(t *testing.T) {
		p := &scriptedRecordPrompter{Err: errors.New("form must not open")}
		if err := runComment(ctx, rig.client, p, []string{rec.ShortID()}, "  from flag  "); err != nil {
			t.Fatalf("runComment: %v", err)
		}
		if got := comments(t); !slices.Equal(got, []string{"from flag"}) {
			t.Errorf("comments = %q", got)
		}
	})

	t.Run("without --message the form supplies the body", func(t *testing.T) {
		p := &scriptedRecordPrompter{Comment: "from form\nsecond line"}
		if err := runComment(ctx, rig.client, p, []string{rec.ID}, ""); err != nil {
			t.Fatalf("runComment: %v", err)
		}
		if got := comments(t); len(got) != 2 || got[1] != "from form\nsecond line" {
			t.Errorf("comments = %q", got)
		}
	})

	t.Run("an empty comment is refused", func(t *testing.T) {
		p := &scriptedRecordPrompter{Comment: "   "}
		err := runComment(ctx, rig.client, p, []string{rec.ID}, "")
		if err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("err = %v", err)
		}
		if got := comments(t); len(got) != 2 {
			t.Errorf("comments = %q", got)
		}
	})
}

func TestRunLabel(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "T", BranchType: "feat", Labels: []string{"old"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	labels := func(t *testing.T) []string {
		t.Helper()

		got, err := issuepkg.Load(ctx, rig.client, rec.ID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		return got.Labels
	}

	t.Run("+name adds and -name removes", func(t *testing.T) {
		if err := runLabel(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ShortID(), "+bug", "-old", "+ui"}); err != nil {
			t.Fatalf("runLabel: %v", err)
		}
		if got := labels(t); !slices.Equal(got, []string{"bug", "ui"}) {
			t.Errorf("labels = %v", got)
		}
		if !strings.Contains(rig.stdout.String(), "labels: bug, ui") {
			t.Errorf("stdout = %q", rig.stdout.String())
		}
	})

	t.Run("without tokens the form supplies them", func(t *testing.T) {
		p := &scriptedRecordPrompter{Labels: "+p1  -ui"}
		if err := runLabel(ctx, rig.client, p, []string{rec.ID}); err != nil {
			t.Fatalf("runLabel: %v", err)
		}
		if got := labels(t); !slices.Equal(got, []string{"bug", "p1"}) {
			t.Errorf("labels = %v", got)
		}
	})

	for name, tokens := range map[string][]string{
		"a token without sign": {"bug"},
		"a bare plus":          {"+"},
		"a bare minus":         {"-"},
		"a blank name":         {"+  "},
	} {
		t.Run(name+" is refused and nothing changes", func(t *testing.T) {
			before := labels(t)
			err := runLabel(ctx, rig.client, &scriptedRecordPrompter{}, append([]string{rec.ID}, tokens...))
			if err == nil || !strings.Contains(err.Error(), "want +name or -name") {
				t.Fatalf("err = %v", err)
			}
			if got := labels(t); !slices.Equal(got, before) {
				t.Errorf("labels changed: %v → %v", before, got)
			}
		})
	}
}

func TestRunSync(t *testing.T) {
	t.Parallel()

	origin := newBareOrigin(t)
	alice := newRecordRig(t, "alice", origin)
	bob := newRecordRig(t, "bob", origin)
	ctx := t.Context()

	if err := runNew(ctx, alice.client, alice.cfg, issuepkg.NewIssue{Title: "Shared"}, nil, nil); err != nil {
		t.Fatalf("runNew: %v", err)
	}
	rec := alice.onlyRecord(t)

	t.Run("sync on another clone brings the issue in", func(t *testing.T) {
		if err := runSync(ctx, bob.client, nil); err != nil {
			t.Fatalf("runSync: %v", err)
		}
		if got := bob.onlyRecord(t); got.ID != rec.ID {
			t.Errorf("bob record = %+v", got)
		}
	})

	t.Run("comments made on both clones before syncing are merged", func(t *testing.T) {
		// Written straight to the store so neither clone pushes yet.
		for c, body := range map[*recordRig]string{alice: "from alice", bob: "from bob"} {
			if err := issuepkg.Append(ctx, c.client, rec.ID, &issuepkg.Op{Type: issuepkg.OpAddComment, Body: body}); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}

		if err := runSync(ctx, alice.client, nil); err != nil {
			t.Fatalf("alice sync: %v", err)
		}
		bob.stdout.Reset()
		if err := runSync(ctx, bob.client, nil); err != nil {
			t.Fatalf("bob sync: %v", err)
		}
		if !strings.Contains(bob.stdout.String(), "1 merged, 1 pushed") {
			t.Errorf("bob stdout = %q", bob.stdout.String())
		}
		if err := runSync(ctx, alice.client, nil); err != nil {
			t.Fatalf("alice second sync: %v", err)
		}

		for name, rig := range map[string]*recordRig{"alice": alice, "bob": bob} {
			got, err := issuepkg.Load(ctx, rig.client, rec.ID)
			if err != nil || len(got.Comments) != 2 {
				t.Errorf("%s sees %d comments (%v)", name, len(got.Comments), err)
			}
		}
	})

	t.Run("sync without a remote reports nothing to do", func(t *testing.T) {
		solo := newRecordRig(t, "solo", "")
		if err := runSync(ctx, solo.client, nil); err != nil {
			t.Fatalf("runSync: %v", err)
		}
		if !strings.Contains(solo.stdout.String(), "0 merged, 0 pushed") {
			t.Errorf("stdout = %q", solo.stdout.String())
		}
	})
}

func TestIssueRootCmd_registersRecordCommands(t *testing.T) {
	t.Parallel()

	root := New(&config.AppConfig{}).GetRootCmd()

	for _, name := range []string{"new", "show", "edit", "comment", "label", "sync"} {
		t.Run("issue "+name+" is registered", func(t *testing.T) {
			t.Parallel()

			sub, _, err := root.Find([]string{name})
			if err != nil || sub.Name() != name {
				t.Errorf("Find(%q) = %v, %v", name, sub, err)
			}
		})
	}
}

// "-wontfix" comes after <id>, so cobra must hand it over as an argument and
// not parse it as flags. Not parallel: the command opens the repository of the
// current directory.
func TestLabelCmd_DashTokenIsNotAFlag(t *testing.T) {
	rig := newRecordRig(t, "alice", "")
	rec, err := issuepkg.Create(t.Context(), rig.client, issuepkg.NewIssue{Title: "T", BranchType: "feat", Labels: []string{"wontfix"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Chdir(rig.dir)

	root := New(rig.cfg).GetRootCmd()
	root.SetArgs([]string{"label", rec.ShortID(), "+bug", "-wontfix"})
	root.SetOut(rig.stdout)
	root.SetErr(rig.stderr)
	execErr := root.ExecuteContext(t.Context())

	t.Run("the command succeeds", func(t *testing.T) {
		if execErr != nil {
			t.Fatalf("Execute: %v", execErr)
		}
	})
	t.Run("the label was removed and the other added", func(t *testing.T) {
		got, err := issuepkg.Load(t.Context(), rig.client, rec.ID)
		if err != nil || !slices.Equal(got.Labels, []string{"bug"}) {
			t.Errorf("labels = %v (%v)", got.Labels, err)
		}
	})
}

func TestRunEdit(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	rec, err := issuepkg.Create(ctx, rig.client,
		issuepkg.NewIssue{Title: "Old title", Description: "Old description", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	load := func(t *testing.T) issuepkg.Record {
		t.Helper()

		got, err := issuepkg.Load(ctx, rig.client, rec.ID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		return got
	}
	opCount := func(t *testing.T) string {
		t.Helper()

		return gitOutput(t, rig.dir, "rev-list", "--count", "refs/zf/issues/"+rec.ID)
	}

	t.Run("--title changes the title only, without the form", func(t *testing.T) {
		p := &scriptedRecordPrompter{Err: errors.New("form must not open")}
		title := "  New title  "
		if err := runEdit(ctx, rig.client, p, []string{rec.ShortID()}, editFlags{title: &title}); err != nil {
			t.Fatalf("runEdit: %v", err)
		}
		got := load(t)
		if got.Title != "New title" || got.Description != "Old description" || got.BranchType != "feat" {
			t.Errorf("record = %+v", got)
		}
		if want := "Edited issue " + rec.ShortID() + ": New title"; !strings.Contains(rig.stdout.String(), want) {
			t.Errorf("stdout = %q, want %q in it", rig.stdout.String(), want)
		}
	})

	t.Run("without flags the form is prefilled and supplies both fields", func(t *testing.T) {
		p := &scriptedRecordPrompter{EditTitle: "Form title", EditDescription: "Form description"}
		if err := runEdit(ctx, rig.client, p, []string{rec.ID}, editFlags{}); err != nil {
			t.Fatalf("runEdit: %v", err)
		}
		if want := [2]string{"New title", "Old description"}; p.EditOffered != want {
			t.Errorf("form prefilled with %q, want %q", p.EditOffered, want)
		}
		if got := load(t); got.Title != "Form title" || got.Description != "Form description" {
			t.Errorf("record = %+v", got)
		}
	})

	t.Run("a form left unchanged writes no op", func(t *testing.T) {
		before := opCount(t)
		p := &scriptedRecordPrompter{EditTitle: "Form title", EditDescription: "Form description"}
		if err := runEdit(ctx, rig.client, p, []string{rec.ID}, editFlags{}); err != nil {
			t.Fatalf("runEdit: %v", err)
		}
		if after := opCount(t); after != before {
			t.Errorf("chain has %s ops, want %s", after, before)
		}
		if !strings.Contains(rig.stdout.String(), "unchanged") {
			t.Errorf("stdout = %q", rig.stdout.String())
		}
	})

	t.Run("an empty --description clears the description", func(t *testing.T) {
		p := &scriptedRecordPrompter{Err: errors.New("form must not open")}
		empty := ""
		if err := runEdit(ctx, rig.client, p, []string{rec.ID}, editFlags{description: &empty}); err != nil {
			t.Fatalf("runEdit: %v", err)
		}
		if got := load(t); got.Title != "Form title" || got.Description != "" {
			t.Errorf("record = %+v", got)
		}
	})

	t.Run("an empty title is refused and nothing changes", func(t *testing.T) {
		before := opCount(t)
		blank := "   "
		err := runEdit(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID}, editFlags{title: &blank})
		if err == nil || !strings.Contains(err.Error(), "title") {
			t.Fatalf("err = %v", err)
		}
		if after := opCount(t); after != before {
			t.Errorf("chain has %s ops, want %s", after, before)
		}
	})
}

// An edit made on one clone reaches the remote.
func TestRunEdit_Pushes(t *testing.T) {
	t.Parallel()

	origin := newBareOrigin(t)
	rig := newRecordRig(t, "alice", origin)
	ctx := t.Context()
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "T", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	title := "Pushed title"
	runErr := runEdit(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID}, editFlags{title: &title})

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runEdit: %v", runErr)
		}
	})
	t.Run("the remote has the edited chain", func(t *testing.T) {
		ref := "refs/zf/issues/" + rec.ID
		if local, remote := gitOutput(t, rig.dir, "rev-parse", ref), gitOutput(t, origin, "rev-parse", ref); local != remote {
			t.Errorf("origin is at %s, local at %s", remote, local)
		}
	})
}

func TestRunCloseByID(t *testing.T) {
	t.Parallel()

	origin := newBareOrigin(t)
	rig := newRecordRig(t, "alice", origin)
	ctx := t.Context()

	create := func(t *testing.T, title string) issuepkg.Record {
		t.Helper()

		rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: title, BranchType: "feat"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		return rec
	}
	state := func(t *testing.T, id string) string {
		t.Helper()

		got, err := issuepkg.Load(ctx, rig.client, id)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		return got.State
	}

	dup := create(t, "Duplicate")

	t.Run("an issue with no branch is closed and pushed", func(t *testing.T) {
		if err := runCloseByID(ctx, rig.client, dup.ShortID(), nil); err != nil {
			t.Fatalf("runCloseByID: %v", err)
		}
		if got := state(t, dup.ID); got != issuepkg.StateClosed {
			t.Errorf("State = %q, want %q", got, issuepkg.StateClosed)
		}
		if want := "Closed issue " + dup.ShortID(); !strings.Contains(rig.stdout.String(), want) {
			t.Errorf("stdout = %q, want %q in it", rig.stdout.String(), want)
		}
		ref := "refs/zf/issues/" + dup.ID
		if local, remote := gitOutput(t, rig.dir, "rev-parse", ref), gitOutput(t, origin, "rev-parse", ref); local != remote {
			t.Errorf("origin is at %s, local at %s", remote, local)
		}
	})

	t.Run("an already closed issue writes no op", func(t *testing.T) {
		ref := "refs/zf/issues/" + dup.ID
		before := gitOutput(t, rig.dir, "rev-parse", ref)
		if err := runCloseByID(ctx, rig.client, dup.ID, nil); err != nil {
			t.Fatalf("runCloseByID: %v", err)
		}
		if after := gitOutput(t, rig.dir, "rev-parse", ref); after != before {
			t.Errorf("chain moved from %s to %s", before, after)
		}
		if !strings.Contains(rig.stdout.String(), "already closed") {
			t.Errorf("stdout = %q", rig.stdout.String())
		}
	})

	t.Run("an issue with a branch in progress is refused", func(t *testing.T) {
		wip := create(t, "In progress")
		name := wip.ShortID() + "@feat@in-progress"
		branchtest.Seed(t, rig.client, branch.Op{Branch: name, Title: wip.Title, IssueID: wip.ID}, branch.StatusInProgress)

		err := runCloseByID(ctx, rig.client, wip.ID, nil)
		if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "branch close") {
			t.Fatalf("err = %v", err)
		}
		if got := state(t, wip.ID); got != issuepkg.StateOpen {
			t.Errorf("State = %q, want %q", got, issuepkg.StateOpen)
		}
	})

	t.Run("a branch started under the short hash before the export is refused too", func(t *testing.T) {
		early := create(t, "Early")
		name := early.ShortID() + "@feat@early"
		branchtest.Seed(t, rig.client, branch.Op{Branch: name, Title: early.Title, IssueID: early.ID}, branch.StatusInProgress)
		link := &issuepkg.Op{Type: issuepkg.OpLinkTracker, TrackerType: "fake", Project: "zf", TrackerID: "11"}
		if err := issuepkg.Append(ctx, rig.client, early.ID, link); err != nil {
			t.Fatalf("Append: %v", err)
		}

		err := runCloseByID(ctx, rig.client, "11", nil)
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("err = %v", err)
		}
		if got := state(t, early.ID); got != issuepkg.StateOpen {
			t.Errorf("State = %q, want %q", got, issuepkg.StateOpen)
		}
	})

	t.Run("an issue whose only branch was abandoned is closed", func(t *testing.T) {
		dropped := create(t, "Abandoned")
		name := dropped.ShortID() + "@feat@abandoned"
		branchtest.Seed(t, rig.client, branch.Op{Branch: name, Title: dropped.Title, IssueID: dropped.ID}, branch.StatusClosed)

		if err := runCloseByID(ctx, rig.client, dropped.ID, nil); err != nil {
			t.Fatalf("runCloseByID: %v", err)
		}
		if got := state(t, dropped.ID); got != issuepkg.StateClosed {
			t.Errorf("State = %q, want %q", got, issuepkg.StateClosed)
		}
	})

	t.Run("an unknown ID is an error", func(t *testing.T) {
		if err := runCloseByID(ctx, rig.client, "ffffffff", nil); err == nil {
			t.Fatal("expected an error, got nil")
		}
	})
}

// `issue close <id>` goes through cobra: the argument selects the close-by-ID
// path, and the merge flags are refused with it. Not parallel: the command
// opens the repository of the current directory.
func TestCloseCmd_ByID(t *testing.T) {
	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Wontfix", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Chdir(rig.dir)

	run := func(args ...string) error {
		root := New(rig.cfg).GetRootCmd()
		root.SetArgs(args)
		root.SetOut(rig.stdout)
		root.SetErr(rig.stderr)

		return root.ExecuteContext(ctx)
	}

	t.Run("a merge flag with an ID is refused and the issue stays open", func(t *testing.T) {
		err := run("close", rec.ShortID(), "--no-push")
		if err == nil || !strings.Contains(err.Error(), "--no-push") {
			t.Fatalf("err = %v", err)
		}
		got, loadErr := issuepkg.Load(ctx, rig.client, rec.ID)
		if loadErr != nil || got.State != issuepkg.StateOpen {
			t.Errorf("State = %q (%v), want %q", got.State, loadErr, issuepkg.StateOpen)
		}
	})

	t.Run("close <id> closes the issue", func(t *testing.T) {
		if err := run("close", rec.ShortID()); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		got, loadErr := issuepkg.Load(ctx, rig.client, rec.ID)
		if loadErr != nil || got.State != issuepkg.StateClosed {
			t.Errorf("State = %q (%v), want %q", got.State, loadErr, issuepkg.StateClosed)
		}
	})
}

// `issue edit <id> --description ""` must clear the description: the flag is
// read as "passed", not as "non-empty". Not parallel: the command opens the
// repository of the current directory.
func TestEditCmd_Flags(t *testing.T) {
	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	rec, err := issuepkg.Create(ctx, rig.client,
		issuepkg.NewIssue{Title: "T", Description: "to clear", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Chdir(rig.dir)

	root := New(rig.cfg).GetRootCmd()
	root.SetArgs([]string{"edit", rec.ShortID(), "--description", ""})
	root.SetOut(rig.stdout)
	root.SetErr(rig.stderr)
	execErr := root.ExecuteContext(ctx)

	t.Run("the command succeeds without opening the form", func(t *testing.T) {
		if execErr != nil {
			t.Fatalf("Execute: %v", execErr)
		}
	})
	t.Run("the description is cleared and the title kept", func(t *testing.T) {
		got, err := issuepkg.Load(ctx, rig.client, rec.ID)
		if err != nil || got.Description != "" || got.Title != "T" {
			t.Errorf("record = %+v (%v)", got, err)
		}
	})
}
