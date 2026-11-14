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

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	issuepkg "github.com/piprim/git-zf/issue"
)

// scriptedRecordPrompter returns canned answers for the repo-issue forms.
type scriptedRecordPrompter struct {
	New issuepkg.NewIssue // copied into the form input by NewIssue
	// NewOffered is the input the `issue new` form opened with.
	NewOffered   issuepkg.NewIssue
	Start        bool   // returned by ConfirmStart
	StartErr     error  // returned by ConfirmStart when non-nil
	StartOffered string // display ID offered to ConfirmStart
	PickID       string // returned by PickRecord; "" picks the first record offered
	Comment      string
	// LabelSet is what LabelChanges returns; LabelOffered records what it, or
	// NewIssue, was offered.
	LabelSet     []string
	LabelOffered struct {
		Choices, Current []string
		Single           bool
	}
	// EditTitle and EditDescription are what EditIssue leaves in the form.
	EditTitle, EditDescription string
	Err                        error // returned by every method when non-nil
	FormErr                    error // returned by the next form alone, then cleared: esc in a form

	Picks       int               // times PickRecord was called
	PickedFrom  []issuepkg.Record // records PickRecord was offered
	EditOffered [2]string         // title and description EditIssue was prefilled with
}

var _ recordPrompter = (*scriptedRecordPrompter)(nil)

// formErr returns FormErr once.
func (s *scriptedRecordPrompter) formErr() error {
	err := s.FormErr
	s.FormErr = nil

	return err
}

func (s *scriptedRecordPrompter) NewIssue(_ context.Context, _, choices []string, single bool, in *issuepkg.NewIssue) error {
	s.LabelOffered.Choices, s.LabelOffered.Single = choices, single
	s.NewOffered = *in
	if s.Err != nil {
		return s.Err
	}
	*in = s.New

	return nil
}

func (s *scriptedRecordPrompter) ConfirmStart(_ context.Context, displayID string) (bool, error) {
	s.StartOffered = displayID
	if s.StartErr != nil {
		return false, s.StartErr
	}
	if s.Err != nil {
		return false, s.Err
	}

	return s.Start, nil
}

func (s *scriptedRecordPrompter) PickRecord(_ context.Context, records []issuepkg.Record) (string, error) {
	s.PickedFrom = records
	s.Picks++
	if s.Err != nil {
		return "", s.Err
	}
	if s.PickID == "" {
		return records[0].ID, nil
	}

	return s.PickID, nil
}

func (s *scriptedRecordPrompter) CommentBody(_ context.Context) (string, error) {
	if err := s.formErr(); err != nil {
		return "", err
	}

	return s.Comment, s.Err
}

func (s *scriptedRecordPrompter) EditIssue(_ context.Context, title, description *string) error {
	if err := s.formErr(); err != nil {
		return err
	}
	if s.Err != nil {
		return s.Err
	}
	s.EditOffered = [2]string{*title, *description}
	*title, *description = s.EditTitle, s.EditDescription

	return nil
}

func (s *scriptedRecordPrompter) LabelChanges(_ context.Context, choices, current []string, single bool) ([]string, error) {
	s.LabelOffered.Choices, s.LabelOffered.Current, s.LabelOffered.Single = choices, current, single
	if err := s.formErr(); err != nil {
		return nil, err
	}

	return s.LabelSet, s.Err
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

	err := runNew(t.Context(), rig.client, rig.cfg, in, nil, nil)

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

	err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{}, p, nil)

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
	t.Run("without a tracker the label form is offered no choices, not single", func(t *testing.T) {
		if p.LabelOffered.Choices != nil || p.LabelOffered.Single {
			t.Errorf("offered = %+v", p.LabelOffered)
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
			err := runNew(t.Context(), rig.client, rig.cfg, tc.in, nil, nil)
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
		if err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{}, p, nil); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("no commit types configured is refused", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		err := runNew(t.Context(), rig.client, &config.AppConfig{}, issuepkg.NewIssue{Title: "T"}, nil, nil)
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

	if err := runNew(t.Context(), alice.client, alice.cfg, issuepkg.NewIssue{Title: "Shared"}, nil, nil); err != nil {
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

	err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{Title: "Offline"}, nil, nil)

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

	err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{Title: "Two remotes"}, nil, nil)

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

func TestRunNew_Assign(t *testing.T) {
	t.Parallel()

	const me = "alice <alice@test.com>"
	ctx := t.Context()

	t.Run("with Assign the issue is assigned to its creator and show says so", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		if err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Mine", Assign: true}, nil, nil); err != nil {
			t.Fatalf("runNew: %v", err)
		}
		rec := rig.onlyRecord(t)
		if rec.Assignee != me {
			t.Errorf("Assignee = %q, want %q", rec.Assignee, me)
		}

		rig.stdout.Reset()
		if err := runShow(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID}, false); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		if want := "Assigned to: " + me + "\n"; !strings.Contains(rig.stdout.String(), want) {
			t.Errorf("output misses %q:\n%s", want, rig.stdout.String())
		}
	})

	t.Run("without it the issue is unassigned and show has no assignee line", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		if err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Nobody's"}, nil, nil); err != nil {
			t.Fatalf("runNew: %v", err)
		}
		rec := rig.onlyRecord(t)
		if rec.Assignee != "" {
			t.Errorf("Assignee = %q, want empty", rec.Assignee)
		}

		rig.stdout.Reset()
		if err := runShow(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID}, false); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		if strings.Contains(rig.stdout.String(), "Assigned to") {
			t.Errorf("output has an assignee line:\n%s", rig.stdout.String())
		}
	})

	t.Run("the form opens proposing to assign, and its answer decides", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		p := &scriptedRecordPrompter{New: issuepkg.NewIssue{Title: "Declined"}}
		if err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{}, p, nil); err != nil {
			t.Fatalf("runNew: %v", err)
		}
		if !p.NewOffered.Assign {
			t.Error("the form opened with Assign off")
		}
		if rec := rig.onlyRecord(t); rec.Assignee != "" {
			t.Errorf("Assignee = %q, want empty: the form said no", rec.Assignee)
		}
	})
}

// Not parallel: the command opens the repository of the current directory.
func TestNewCmd_Assign(t *testing.T) {
	rig := newRecordRig(t, "alice", "")
	t.Chdir(rig.dir)

	root := New(rig.cfg).GetRootCmd()
	root.SetArgs([]string{"new", "--title", "Mine", "--assign"})
	root.SetOut(rig.stdout)
	root.SetErr(rig.stderr)
	execErr := root.ExecuteContext(t.Context())

	t.Run("the command succeeds", func(t *testing.T) {
		if execErr != nil {
			t.Fatalf("Execute: %v", execErr)
		}
	})
	t.Run("--assign assigns the issue to its creator", func(t *testing.T) {
		if rec := rig.onlyRecord(t); rec.Assignee != "alice <alice@test.com>" {
			t.Errorf("Assignee = %q", rec.Assignee)
		}
	})
}

func TestRunNew_ProposeStart(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	formNew := issuepkg.NewIssue{Title: "From form", BranchType: "fix"}

	t.Run("declining the start proposal leaves no branch and no error", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		p := &scriptedRecordPrompter{New: formNew, Start: false}
		sp := &scriptedStartPrompter{ConfirmBranch: true}

		started, err := runNewWithStart(ctx, issueflow.StartDeps{Client: rig.client, Cfg: rig.cfg}, issuepkg.NewIssue{}, p, sp)
		if err != nil || started {
			t.Fatalf("started = %v, err = %v", started, err)
		}
		rec := rig.onlyRecord(t)
		if p.StartOffered != rec.ShortID() {
			t.Errorf("StartOffered = %q, want %q", p.StartOffered, rec.ShortID())
		}
		if out := gitOutput(t, rig.dir, "branch", "--format=%(refname:short)"); out != "main" {
			t.Errorf("branches = %q, want only main", out)
		}
	})

	t.Run("accepting the start proposal creates and checks out the issue branch", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		p := &scriptedRecordPrompter{New: formNew, Start: true}
		sp := &scriptedStartPrompter{ConfirmBranch: true}

		started, err := runNewWithStart(ctx, issueflow.StartDeps{Client: rig.client, Cfg: rig.cfg}, issuepkg.NewIssue{}, p, sp)
		if err != nil || !started {
			t.Fatalf("started = %v, err = %v", started, err)
		}
		rec := rig.onlyRecord(t)
		wantBranch := rec.ShortID() + "@fix@from-form"
		exists, err := rig.client.BranchExists(wantBranch)
		if err != nil || !exists {
			t.Errorf("branch %q exists = %v (%v)", wantBranch, exists, err)
		}
		ref, err := branch.Load(ctx, rig.client, rec.ShortID())
		if err != nil || ref == nil || ref.IssueID != rec.ID || ref.Entry(wantBranch) == nil {
			t.Errorf("ref = %+v (%v)", ref, err)
		}
	})

	t.Run("esc on the start proposal keeps the created issue and returns no error", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		p := &scriptedRecordPrompter{New: formNew, StartErr: huh.ErrUserAborted}
		sp := &scriptedStartPrompter{ConfirmBranch: true}

		started, err := runNewWithStart(ctx, issueflow.StartDeps{Client: rig.client, Cfg: rig.cfg}, issuepkg.NewIssue{}, p, sp)
		if err != nil || started {
			t.Fatalf("started = %v, err = %v", started, err)
		}
		if rec := rig.onlyRecord(t); rec.Title != "From form" {
			t.Errorf("record = %+v", rec)
		}
		if out := gitOutput(t, rig.dir, "branch", "--format=%(refname:short)"); out != "main" {
			t.Errorf("branches = %q, want only main", out)
		}
	})

	t.Run("declining the branch confirm keeps the created issue", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		p := &scriptedRecordPrompter{New: formNew, Start: true}
		sp := &scriptedStartPrompter{ConfirmBranch: false}

		started, err := runNewWithStart(ctx, issueflow.StartDeps{Client: rig.client, Cfg: rig.cfg}, issuepkg.NewIssue{}, p, sp)
		if err != nil || !started {
			t.Fatalf("started = %v, err = %v", started, err)
		}
		if rec := rig.onlyRecord(t); rec.Title != "From form" {
			t.Errorf("record = %+v", rec)
		}
		if out := gitOutput(t, rig.dir, "branch", "--format=%(refname:short)"); out != "main" {
			t.Errorf("branches = %q, want only main", out)
		}
	})

	t.Run("esc inside the start flow keeps the created issue and returns no error", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		p := &scriptedRecordPrompter{New: formNew, Start: true}
		sp := &scriptedStartPrompter{ConfirmBranchErr: huh.ErrUserAborted}

		started, err := runNewWithStart(ctx, issueflow.StartDeps{Client: rig.client, Cfg: rig.cfg}, issuepkg.NewIssue{}, p, sp)
		if err != nil || started {
			t.Fatalf("started = %v, err = %v", started, err)
		}
		if rec := rig.onlyRecord(t); rec.Title != "From form" {
			t.Errorf("record = %+v", rec)
		}
		if out := gitOutput(t, rig.dir, "branch", "--format=%(refname:short)"); out != "main" {
			t.Errorf("branches = %q, want only main", out)
		}
	})

	t.Run("a nil start prompter never asks to start", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		p := &scriptedRecordPrompter{New: formNew, Start: true}

		started, err := runNewWithStart(ctx, issueflow.StartDeps{Client: rig.client, Cfg: rig.cfg}, issuepkg.NewIssue{}, p, nil)
		if err != nil || started {
			t.Fatalf("started = %v, err = %v", started, err)
		}
		if p.StartOffered != "" {
			t.Errorf("StartOffered = %q, want empty", p.StartOffered)
		}
		if out := gitOutput(t, rig.dir, "branch", "--format=%(refname:short)"); out != "main" {
			t.Errorf("branches = %q, want only main", out)
		}
	})

	t.Run("flag-driven run never asks to start", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		sp := &scriptedStartPrompter{ConfirmBranch: true}

		started, err := runNewWithStart(ctx, issueflow.StartDeps{Client: rig.client, Cfg: rig.cfg}, issuepkg.NewIssue{Title: "Flagged"}, nil, sp)
		if err != nil || started {
			t.Fatalf("started = %v, err = %v", started, err)
		}
		if rec := rig.onlyRecord(t); rec.Title != "Flagged" {
			t.Errorf("record = %+v", rec)
		}
		if out := gitOutput(t, rig.dir, "branch", "--format=%(refname:short)"); out != "main" {
			t.Errorf("branches = %q, want only main", out)
		}
	})

	t.Run("a non-abort error on the start proposal is returned after creating the issue", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		p := &scriptedRecordPrompter{New: formNew, StartErr: errors.New("boom")}
		sp := &scriptedStartPrompter{ConfirmBranch: true}

		started, err := runNewWithStart(ctx, issueflow.StartDeps{Client: rig.client, Cfg: rig.cfg}, issuepkg.NewIssue{}, p, sp)
		if err == nil || !strings.Contains(err.Error(), "confirm start: boom") || started {
			t.Fatalf("started = %v, err = %v", started, err)
		}
		if rec := rig.onlyRecord(t); rec.Title != "From form" {
			t.Errorf("record = %+v", rec)
		}
	})

	t.Run("a non-abort error inside the start flow is returned after creating the issue", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		p := &scriptedRecordPrompter{New: formNew, Start: true}
		sp := &scriptedStartPrompter{ConfirmBranchErr: errors.New("boom")}

		started, err := runNewWithStart(ctx, issueflow.StartDeps{Client: rig.client, Cfg: rig.cfg}, issuepkg.NewIssue{}, p, sp)
		if err == nil || !strings.Contains(err.Error(), "start issue:") || !strings.Contains(err.Error(), "boom") || started {
			t.Fatalf("started = %v, err = %v", started, err)
		}
		if rec := rig.onlyRecord(t); rec.Title != "From form" {
			t.Errorf("record = %+v", rec)
		}
	})
}
