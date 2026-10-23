package issue

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// An empty issue ID in the manual form creates a repo issue and names the
// branch after its short ID.
func TestRunIssueStart_ManualEmptyIDCreatesRepoIssue(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	prompter := &scriptedStartPrompter{
		IssueFromUser: &issuepkg.Issue{Type: "fix", Issue: tracker.Issue{Subject: "Login fails"}},
		ConfirmBranch: true,
	}

	err := issueflow.RunIssueStart(t.Context(), rig.noTrackerDeps(issuepkg.IssueStartFlags{}), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("RunIssueStart: %v", err)
		}
	})

	records, _, listErr := issuepkg.List(t.Context(), rig.client)
	if listErr != nil || len(records) != 1 {
		t.Fatalf("want one repo issue, got %d (%v)", len(records), listErr)
	}
	rec := records[0]
	wantBranch := rec.ShortID() + "@fix@login-fails"

	t.Run("the repo issue holds the title and type", func(t *testing.T) {
		if rec.Title != "Login fails" || rec.BranchType != "fix" || rec.State != issuepkg.StateOpen {
			t.Errorf("record = %+v", rec)
		}
	})
	t.Run("the branch is named after the short ID", func(t *testing.T) {
		exists, err := rig.client.BranchExists(wantBranch)
		if err != nil || !exists {
			t.Errorf("branch %q exists = %v (%v)", wantBranch, exists, err)
		}
	})
	t.Run("the branch ref records the full issue ID", func(t *testing.T) {
		ref, err := branch.Load(t.Context(), rig.client, rec.ShortID())
		if err != nil || ref == nil {
			t.Fatalf("Load = %+v, %v", ref, err)
		}
		if ref.IssueID != rec.ID || ref.Entry(wantBranch) == nil {
			t.Errorf("ref = %+v", ref)
		}
	})
}

// branch new opens the manual form at once, issues in the repository or
// not: it creates a branch, never picks an issue. A typed ID is kept as is
// and creates no record.
func TestRunIssueStart_OpensTheFormWithIssuesInTheRepo(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()

	if _, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Existing", BranchType: "feat"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	prompter := &scriptedStartPrompter{
		IssueFromUser: &issuepkg.Issue{Type: "feat", Issue: tracker.Issue{ID: "JIRA-7", Subject: "Typed"}},
		ConfirmBranch: true,
	}

	err := issueflow.RunIssueStart(ctx, rig.noTrackerDeps(issuepkg.IssueStartFlags{}), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("RunIssueStart: %v", err)
		}
	})
	t.Run("the typed ID names the branch", func(t *testing.T) {
		exists, err := rig.client.BranchExists("JIRA-7@feat@typed")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
	})
	t.Run("no repo issue is created for a typed ID", func(t *testing.T) {
		records, _, _ := issuepkg.List(ctx, rig.client)
		if len(records) != 1 {
			t.Errorf("repo has %d issues, want 1", len(records))
		}
	})
	t.Run("the branch ref has no issue ID", func(t *testing.T) {
		ref, err := branch.Load(ctx, rig.client, "JIRA-7")
		if err != nil || ref == nil || ref.IssueID != "" {
			t.Errorf("ref = %+v, %v", ref, err)
		}
	})
}

// With a tracker configured the path is the same manual one: an empty ID
// creates a repo issue instead of producing a branch with no issue ID, and
// the tracker is never listed.
func TestRunIssueStart_TrackerConfiguredCreatesRepoIssue(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	rig.tracker.ProjectIssues = []tracker.Issue{{ID: "ABC-11", Subject: "Remote", TrackerType: "fake"}}

	prompter := &scriptedStartPrompter{
		IssueFromUser: &issuepkg.Issue{Type: "feat", Issue: tracker.Issue{Subject: "Fallback"}},
		ConfirmBranch: true,
	}

	err := issueflow.RunIssueStart(t.Context(), rig.deps(issuepkg.IssueStartFlags{Tracker: true}), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("RunIssueStart: %v", err)
		}
	})
	t.Run("the tracker was never listed", func(t *testing.T) {
		if rig.tracker.ListCalls != 0 {
			t.Errorf("ListCalls = %d", rig.tracker.ListCalls)
		}
	})
	t.Run("a repo issue was created and names the branch", func(t *testing.T) {
		records, _, _ := issuepkg.List(t.Context(), rig.client)
		if len(records) != 1 {
			t.Fatalf("repo has %d issues, want 1", len(records))
		}
		want := records[0].ShortID() + "@feat@fallback"
		exists, err := rig.client.BranchExists(want)
		if err != nil || !exists {
			t.Errorf("branch %q exists = %v (%v)", want, exists, err)
		}
	})
}

// A title that slugs to nothing (non-Latin script) still gets a branch: the
// slug segment falls back to branch.FallbackSlug and the title stays whole in
// the issue and in the branch chain.
func TestRunIssueStart_NonLatinTitleFallsBackToIssueSlug(t *testing.T) {
	t.Parallel()

	const title = "日本語のタイトル"

	rig := newStartRig(t)
	prompter := &scriptedStartPrompter{
		IssueFromUser: &issuepkg.Issue{Type: "feat", Issue: tracker.Issue{Subject: title}},
		ConfirmBranch: true,
	}

	err := issueflow.RunIssueStart(t.Context(), rig.noTrackerDeps(issuepkg.IssueStartFlags{}), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("RunIssueStart: %v", err)
		}
	})

	records, _, listErr := issuepkg.List(t.Context(), rig.client)
	if listErr != nil || len(records) != 1 {
		t.Fatalf("want one repo issue, got %d (%v)", len(records), listErr)
	}
	rec := records[0]
	wantBranch := rec.ShortID() + "@feat@" + branch.FallbackSlug

	t.Run("the repo issue keeps the title", func(t *testing.T) {
		if rec.Title != title {
			t.Errorf("Title = %q, want %q", rec.Title, title)
		}
	})
	t.Run("the branch uses the fallback slug", func(t *testing.T) {
		exists, err := rig.client.BranchExists(wantBranch)
		if err != nil || !exists {
			t.Errorf("branch %q exists = %v (%v)", wantBranch, exists, err)
		}
	})
	t.Run("the branch chain keeps the title", func(t *testing.T) {
		ref, err := branch.Load(t.Context(), rig.client, rec.ShortID())
		if err != nil || ref == nil {
			t.Fatalf("Load = %+v, %v", ref, err)
		}
		if ref.Title != title || ref.Entry(wantBranch) == nil {
			t.Errorf("ref = %+v", ref)
		}
	})
}

// A backlog item created by `issue new` with a non-Latin title can be started.
func TestStartRow_NonLatinBacklogItem(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()

	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Проблема входа", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	flags := issuepkg.IssueStartFlags{}
	row := rowOf(t, rig, flags, rec.ShortID())
	prompter := &scriptedStartPrompter{ConfirmBranch: true}

	runErr := issueflow.StartRow(ctx, rig.noTrackerDeps(flags), prompter, row)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("StartRow: %v", runErr)
		}
	})
	t.Run("the branch uses the fallback slug", func(t *testing.T) {
		want := rec.ShortID() + "@fix@" + branch.FallbackSlug
		exists, err := rig.client.BranchExists(want)
		if err != nil || !exists {
			t.Errorf("branch %q exists = %v (%v)", want, exists, err)
		}
	})
}

// Declining the "Create branch?" confirm must not leave an issue behind: the
// issue is only written once the branch exists.
func TestRunIssueStart_AbortAtConfirmLeavesNoIssue(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	origin := newBareOrigin(t)
	rig.runGit(t, "remote", "add", "origin", origin)

	prompter := &scriptedStartPrompter{
		IssueFromUser: &issuepkg.Issue{Type: "feat", Issue: tracker.Issue{Subject: "Changed my mind"}},
		ConfirmBranch: false,
	}

	err := issueflow.RunIssueStart(t.Context(), rig.noTrackerDeps(issuepkg.IssueStartFlags{}), prompter)

	t.Run("the abort is not an error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("RunIssueStart: %v", err)
		}
	})
	t.Run("no repo issue exists locally", func(t *testing.T) {
		records, _, _ := issuepkg.List(t.Context(), rig.client)
		if len(records) != 0 {
			t.Errorf("repo has %d issues, want 0", len(records))
		}
	})
	t.Run("nothing was pushed to the remote", func(t *testing.T) {
		if out := gitOutput(t, origin, "for-each-ref", "refs/zf/issues"); out != "" {
			t.Errorf("origin has issue refs: %q", out)
		}
	})
}

// On success the new issue exists and is pushed, after the branch was created.
func TestRunIssueStart_NewRepoIssueIsPushedOnceBranchExists(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	origin := newBareOrigin(t)
	rig.runGit(t, "remote", "add", "origin", origin)

	prompter := &scriptedStartPrompter{
		IssueFromUser: &issuepkg.Issue{Type: "feat", Issue: tracker.Issue{Subject: "Keep it"}},
		ConfirmBranch: true,
	}

	if err := issueflow.RunIssueStart(t.Context(), rig.noTrackerDeps(issuepkg.IssueStartFlags{}), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	records, _, _ := issuepkg.List(t.Context(), rig.client)
	if len(records) != 1 {
		t.Fatalf("repo has %d issues, want 1", len(records))
	}

	t.Run("the remote has the issue ref", func(t *testing.T) {
		out := gitOutput(t, origin, "for-each-ref", "--format=%(refname)", "refs/zf/issues")
		if out != "refs/zf/issues/"+records[0].ID {
			t.Errorf("origin issue refs = %q", out)
		}
	})
}

// gitOutput runs git in dir and returns its trimmed stdout.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}

	return strings.TrimSpace(string(out))
}

// Commit types are per-clone config, and a record's type comes from the
// remote: an issue whose type this clone does not know (or that is empty or
// malformed) must be refused with a message naming the issue and the type,
// before any branch is created.
func TestStartRow_RepoIssueWithUnknownTypeIsRefused(t *testing.T) {
	t.Parallel()

	for name, branchType := range map[string]string{
		"a type this clone does not configure": "perf",
		"an empty type":                        "",
		"a type holding the branch separator":  "fe@t",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rig := newStartRig(t) // configures feat and fix
			ctx := t.Context()

			rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Odd type", BranchType: branchType})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			flags := issuepkg.IssueStartFlags{}
			row := rowOf(t, rig, flags, rec.ShortID())
			prompter := &scriptedStartPrompter{ConfirmBranch: true}
			runErr := issueflow.StartRow(ctx, rig.noTrackerDeps(flags), prompter, row)

			t.Run("the flow fails naming the issue, its type and the allowed ones", func(t *testing.T) {
				if runErr == nil {
					t.Fatal("expected an error, got nil")
				}
				for _, want := range []string{rec.ShortID(), `"` + branchType + `"`, "feat, fix"} {
					if !strings.Contains(runErr.Error(), want) {
						t.Errorf("error %q does not mention %q", runErr.Error(), want)
					}
				}
			})
			t.Run("no branch is created", func(t *testing.T) {
				if out := gitOutput(t, rig.dir, "branch", "--format=%(refname:short)"); out != "main" {
					t.Errorf("branches = %q, want only main", out)
				}
			})
		})
	}
}

// The branch must be recorded in the repository the flow works on, not in the
// one the process was started from: under `go test` that is git-zf itself,
// whose `issue close` picker would then list every test fixture.
func TestRunIssueStart_RecordsBranchInTheFlowRepo(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	prompter := &scriptedStartPrompter{
		IssueFromUser: &issuepkg.Issue{Type: "feat", Issue: tracker.Issue{ID: "STORE-1", Subject: "Store location"}},
		ConfirmBranch: true,
	}

	err := issueflow.RunIssueStart(t.Context(), rig.noTrackerDeps(issuepkg.IssueStartFlags{}), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("RunIssueStart: %v", err)
		}
	})
	t.Run("no record warning", func(t *testing.T) {
		if strings.Contains(rig.stderr.String(), "record failed") {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})
	t.Run("the branch is tracked in the flow's repository", func(t *testing.T) {
		rows, err := branch.ListRows(t.Context(), rig.client, branch.StatusAll)
		if err != nil {
			t.Fatalf("ListRows: %v", err)
		}
		if len(rows) != 1 || rows[0].BranchName != "STORE-1@feat@store-location" || rows[0].IssueSlug != "STORE-1" {
			t.Errorf("rows = %+v, want the single STORE-1 branch", rows)
		}
	})
}

// An issue imported by the mirror has no branch type. Started from the repo
// picker, it asks the type with the tracker form and gets the branch a live
// tracker listing would have created.
// A record exported to the tracker names its branch by its tracker number,
// like an imported one; the chain still names the record.
func TestStartRow_ExportedRecordIsNamedByItsNumber(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()

	created, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Exported", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	m := &issuepkg.Mirror{Tracker: &fake.Tracker{}, Type: "fake", Project: "zf"}
	if _, err := m.Reconcile(ctx, rig.client); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	exported, err := issuepkg.Load(ctx, rig.client, created.ID)
	if err != nil || exported.Tracker == nil {
		t.Fatalf("Load: %+v, %v", exported, err)
	}

	flags := issuepkg.IssueStartFlags{}
	row := rowOf(t, rig, flags, exported.DisplayID())
	prompter := &scriptedStartPrompter{ConfirmBranch: true}

	runErr := issueflow.StartRow(ctx, rig.noTrackerDeps(flags), prompter, row)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("StartRow: %v", runErr)
		}
	})
	t.Run("the branch is named after the tracker number", func(t *testing.T) {
		const want = "1@feat@exported"
		exists, err := rig.client.BranchExists(want)
		if err != nil || !exists {
			t.Errorf("branch %q exists = %v (%v)", want, exists, err)
		}
	})
	t.Run("the chain under the number records the full record ID", func(t *testing.T) {
		ref, err := branch.Load(ctx, rig.client, "1")
		if err != nil || ref == nil || ref.IssueID != created.ID {
			t.Errorf("ref = %+v, %v", ref, err)
		}
	})
}

func TestStartRow_ImportedRecordStartsAsTrackerIssue(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	rig.tracker.ProjectIssues = nil // the tracker lists nothing: the imported record is the only row

	source := &fake.Tracker{ProjectIssues: []tracker.Issue{{
		TrackerType: "fake", ID: "42", Subject: "From tracker", Status: "open",
		CreatedAt: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
	}}}
	m := &issuepkg.Mirror{Tracker: source, Type: "fake", Project: "zf"}
	if _, err := m.Reconcile(ctx, rig.client); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	records, _, _ := issuepkg.List(ctx, rig.client)
	if len(records) != 1 {
		t.Fatalf("repo has %d issues, want 1", len(records))
	}
	imported := records[0]

	flags := issuepkg.IssueStartFlags{Tracker: true}
	row := rowOf(t, rig, flags, imported.DisplayID())
	prompter := &scriptedStartPrompter{
		IssueFromTracker: &issuepkg.Issue{Type: "fix"},
		ConfirmBranch:    true,
	}

	runErr := issueflow.StartRow(ctx, rig.deps(flags), prompter, row)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("StartRow: %v", runErr)
		}
	})
	t.Run("the branch is named after the tracker number and the picked type", func(t *testing.T) {
		const want = "42@fix@from-tracker"
		exists, err := rig.client.BranchExists(want)
		if err != nil || !exists {
			t.Errorf("branch %q exists = %v (%v)", want, exists, err)
		}
	})
	t.Run("the branch chain records the tracker origin and the issue ID", func(t *testing.T) {
		ref, err := branch.Load(ctx, rig.client, "42")
		if err != nil || ref == nil || ref.TrackerType != rig.cfg.IssueTracker.Type || ref.IssueID != imported.ID {
			t.Errorf("ref = %+v, %v", ref, err)
		}
	})
	t.Run("no second issue is created", func(t *testing.T) {
		records, _, _ := issuepkg.List(ctx, rig.client)
		if len(records) != 1 {
			t.Errorf("repo has %d issues, want 1", len(records))
		}
	})
}
