package issue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	issuepkg "github.com/piprim/git-zf/issue"
)

// scriptedRecordPrompter returns canned answers for the repo-issue forms.
type scriptedRecordPrompter struct {
	New     issuepkg.NewIssue // copied into the form input by NewIssue
	PickID  string            // returned by PickRecord; "" picks the first record offered
	Comment string
	Labels  string
	// EditTitle and EditDescription are what EditIssue leaves in the form.
	EditTitle, EditDescription string
	Err                        error // returned by every method when non-nil

	PickedFrom  []issuepkg.Record // records PickRecord was offered
	EditOffered [2]string         // title and description EditIssue was prefilled with
}

var _ recordPrompter = (*scriptedRecordPrompter)(nil)

func (s *scriptedRecordPrompter) NewIssue(_ context.Context, _ []string, in *issuepkg.NewIssue) error {
	if s.Err != nil {
		return s.Err
	}
	*in = s.New

	return nil
}

func (s *scriptedRecordPrompter) PickRecord(_ context.Context, records []issuepkg.Record) (string, error) {
	s.PickedFrom = records
	if s.Err != nil {
		return "", s.Err
	}
	if s.PickID == "" {
		return records[0].ID, nil
	}

	return s.PickID, nil
}

func (s *scriptedRecordPrompter) CommentBody(_ context.Context) (string, error) {
	return s.Comment, s.Err
}

func (s *scriptedRecordPrompter) EditIssue(_ context.Context, title, description *string) error {
	if s.Err != nil {
		return s.Err
	}
	s.EditOffered = [2]string{*title, *description}
	*title, *description = s.EditTitle, s.EditDescription

	return nil
}

func (s *scriptedRecordPrompter) LabelChanges(_ context.Context) (string, error) {
	return s.Labels, s.Err
}

// recordRig is a real on-disk repo for the repo-issue commands.
type recordRig struct {
	dir    string
	client *git.Client
	cfg    *config.AppConfig
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

// newRecordRig creates a repo with one commit. origin, when non-empty, is
// added as the "origin" remote.
func newRecordRig(t *testing.T, user, origin string) *recordRig {
	t.Helper()

	dir := t.TempDir()
	runGitIn(t, dir, "init", "-q", "-b", "main")
	runGitIn(t, dir, "config", "user.name", user)
	runGitIn(t, dir, "config", "user.email", user+"@test.com")
	runGitIn(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runGitIn(t, dir, "add", "base.txt")
	runGitIn(t, dir, "commit", "-q", "-m", "chore: init")
	if origin != "" {
		runGitIn(t, dir, "remote", "add", "origin", origin)
	}

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	client, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.CommitTypes = []config.CommitTypeOption{{Name: "feat"}, {Name: "fix"}}

	return &recordRig{dir: dir, client: client, cfg: cfg, stdout: stdout, stderr: stderr}
}

func newBareOrigin(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "origin.git")
	runGitIn(t, t.TempDir(), "init", "-q", "--bare", dir)

	return dir
}

// onlyRecord returns the single issue of the rig's repo.
func (r *recordRig) onlyRecord(t *testing.T) issuepkg.Record {
	t.Helper()

	records, _, err := issuepkg.List(t.Context(), r.client)
	if err != nil || len(records) != 1 {
		t.Fatalf("want exactly one issue, got %d (%v)", len(records), err)
	}

	return records[0]
}

func TestRunNew_Flags(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	in := issuepkg.NewIssue{Title: "  Login fails  ", Description: "Steps", Labels: []string{"ui", " ", "ui", "bug"}}

	err := runNew(t.Context(), rig.client, rig.cfg, in, nil)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runNew: %v", err)
		}
	})

	rec := rig.onlyRecord(t)

	t.Run("the record holds the trimmed title, the description and an open state", func(t *testing.T) {
		if rec.Title != "Login fails" || rec.Description != "Steps" || rec.State != issuepkg.StateOpen {
			t.Errorf("record = %+v", rec)
		}
	})
	t.Run("the type defaults to the first commit type", func(t *testing.T) {
		if rec.BranchType != "feat" {
			t.Errorf("BranchType = %q", rec.BranchType)
		}
	})
	t.Run("labels are trimmed and de-duplicated", func(t *testing.T) {
		if !slices.Equal(rec.Labels, []string{"bug", "ui"}) {
			t.Errorf("Labels = %v", rec.Labels)
		}
	})
	t.Run("the display ID is printed", func(t *testing.T) {
		if want := "Created issue " + rec.DisplayID() + ": Login fails"; !strings.Contains(rig.stdout.String(), want) {
			t.Errorf("stdout = %q, want it to contain %q", rig.stdout.String(), want)
		}
	})
	t.Run("no warning without a remote", func(t *testing.T) {
		if rig.stderr.Len() != 0 {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})
}

func TestRunNew_Form(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	p := &scriptedRecordPrompter{New: issuepkg.NewIssue{Title: "From form", BranchType: "fix"}}

	err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{}, p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runNew: %v", err)
		}
	})
	t.Run("the record comes from the form answers", func(t *testing.T) {
		rec := rig.onlyRecord(t)
		if rec.Title != "From form" || rec.BranchType != "fix" {
			t.Errorf("record = %+v", rec)
		}
	})
}

func TestRunNew_Rejections(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		in   issuepkg.NewIssue
		want string
	}{
		"empty title":      {issuepkg.NewIssue{Title: "   "}, "title is required"},
		"unknown type":     {issuepkg.NewIssue{Title: "T", BranchType: "nope"}, `unknown type "nope"`},
		"whitespace title": {issuepkg.NewIssue{Title: "\t\n"}, "title is required"},
	} {
		t.Run(name+" is refused and nothing is written", func(t *testing.T) {
			t.Parallel()

			rig := newRecordRig(t, "alice", "")
			err := runNew(t.Context(), rig.client, rig.cfg, tc.in, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if records, _, _ := issuepkg.List(t.Context(), rig.client); len(records) != 0 {
				t.Errorf("an issue was created: %+v", records)
			}
		})
	}

	t.Run("a form error aborts", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		p := &scriptedRecordPrompter{Err: errors.New("user aborted")}
		if err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{}, p); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("no commit types configured is refused", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		err := runNew(t.Context(), rig.client, &config.AppConfig{}, issuepkg.NewIssue{Title: "T"}, nil)
		if err == nil || !strings.Contains(err.Error(), "no commit types") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRunNew_PushesToRemote(t *testing.T) {
	t.Parallel()

	origin := newBareOrigin(t)
	alice := newRecordRig(t, "alice", origin)
	bob := newRecordRig(t, "bob", origin)

	if err := runNew(t.Context(), alice.client, alice.cfg, issuepkg.NewIssue{Title: "Shared"}, nil); err != nil {
		t.Fatalf("runNew: %v", err)
	}
	rec := alice.onlyRecord(t)

	t.Run("another clone sees the issue with show", func(t *testing.T) {
		if err := runShow(t.Context(), bob.client, &scriptedRecordPrompter{}, []string{rec.ShortID()}, false); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		if !strings.Contains(bob.stdout.String(), "Shared") {
			t.Errorf("stdout = %q", bob.stdout.String())
		}
	})
}

func TestRunNew_UnreachableRemoteWarns(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", filepath.Join(t.TempDir(), "missing.git"))

	err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{Title: "Offline"}, nil)

	t.Run("the command still succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runNew: %v", err)
		}
	})
	t.Run("the issue exists locally", func(t *testing.T) {
		if rec := rig.onlyRecord(t); rec.Title != "Offline" {
			t.Errorf("record = %+v", rec)
		}
	})
	t.Run("a warning says it was not pushed", func(t *testing.T) {
		if !strings.Contains(rig.stderr.String(), "not pushed") {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})
}

func TestRunShow(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{
		Title: "Login fails", Description: "Steps to reproduce", BranchType: "fix", Labels: []string{"ui"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := issuepkg.Append(ctx, rig.client, rec.ID, &issuepkg.Op{Type: issuepkg.OpAddComment, Body: "me too"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	t.Run("plain output shows title, state, labels, description and comments", func(t *testing.T) {
		rig.stdout.Reset()
		if err := runShow(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID}, false); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		out := rig.stdout.String()
		for _, want := range []string{rec.ShortID() + "  Login fails", "State: open", "Type: fix", "Labels: ui", "Steps to reproduce", "me too", "alice <alice@test.com>", "ID: " + rec.ID} {
			if !strings.Contains(out, want) {
				t.Errorf("output misses %q:\n%s", want, out)
			}
		}
	})

	t.Run("--json emits the record", func(t *testing.T) {
		rig.stdout.Reset()
		if err := runShow(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ShortID()}, true); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		var got issuepkg.Record
		if err := json.Unmarshal(rig.stdout.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal %q: %v", rig.stdout.String(), err)
		}
		if got.ID != rec.ID || got.Title != "Login fails" || len(got.Comments) != 1 {
			t.Errorf("json record = %+v", got)
		}
	})

	t.Run("without an ID the picker is used", func(t *testing.T) {
		rig.stdout.Reset()
		p := &scriptedRecordPrompter{}
		if err := runShow(ctx, rig.client, p, nil, false); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		if len(p.PickedFrom) != 1 || !strings.Contains(rig.stdout.String(), "Login fails") {
			t.Errorf("picker offered %d records, stdout = %q", len(p.PickedFrom), rig.stdout.String())
		}
	})

	t.Run("an unknown ID is an error", func(t *testing.T) {
		err := runShow(ctx, rig.client, &scriptedRecordPrompter{}, []string{"ffffffff"}, false)
		if !errors.Is(err, git.ErrIssueNotFound) {
			t.Errorf("err = %v, want ErrIssueNotFound", err)
		}
	})
}

func TestRunShow_NoIssues(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")

	t.Run("without an ID and without issues the error points at issue new", func(t *testing.T) {
		err := runShow(t.Context(), rig.client, &scriptedRecordPrompter{}, nil, false)
		if err == nil || !strings.Contains(err.Error(), "issue new") {
			t.Errorf("err = %v", err)
		}
	})
}

// With several remotes and none named "origin", git-zf cannot pick one. The
// issue must still be saved locally, with a warning.
func TestRunNew_AmbiguousRemoteWarns(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	runGitIn(t, rig.dir, "remote", "add", "upstream", newBareOrigin(t))
	runGitIn(t, rig.dir, "remote", "add", "fork", newBareOrigin(t))

	err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{Title: "Two remotes"}, nil)

	t.Run("the command still succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runNew: %v", err)
		}
	})
	t.Run("the issue exists locally", func(t *testing.T) {
		if rec := rig.onlyRecord(t); rec.Title != "Two remotes" {
			t.Errorf("record = %+v", rec)
		}
	})
	t.Run("the warning names the remote problem", func(t *testing.T) {
		if !strings.Contains(rig.stderr.String(), "multiple remotes") {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})
}
