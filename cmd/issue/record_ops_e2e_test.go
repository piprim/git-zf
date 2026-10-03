package issue

import (
	"errors"
	"slices"
	"strings"
	"testing"

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

	if err := runNew(ctx, alice.client, alice.cfg, issuepkg.NewIssue{Title: "Shared"}, nil); err != nil {
		t.Fatalf("runNew: %v", err)
	}
	rec := alice.onlyRecord(t)

	t.Run("sync on another clone brings the issue in", func(t *testing.T) {
		if err := runSync(ctx, bob.client); err != nil {
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

		if err := runSync(ctx, alice.client); err != nil {
			t.Fatalf("alice sync: %v", err)
		}
		bob.stdout.Reset()
		if err := runSync(ctx, bob.client); err != nil {
			t.Fatalf("bob sync: %v", err)
		}
		if !strings.Contains(bob.stdout.String(), "1 merged, 1 pushed") {
			t.Errorf("bob stdout = %q", bob.stdout.String())
		}
		if err := runSync(ctx, alice.client); err != nil {
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
		if err := runSync(ctx, solo.client); err != nil {
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

	for _, name := range []string{"new", "show", "comment", "label", "sync"} {
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
