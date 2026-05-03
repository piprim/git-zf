package branch

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/piprim/git-zf/store"
)

// fakePruner implements branchPruner for tests without a real git repository.
type fakePruner struct {
	base       string
	localNames []string
	mergedSet  map[string]bool
}

func (f *fakePruner) DefaultBaseBranch() (string, error)        { return f.base, nil }
func (f *fakePruner) LocalBranchNames() ([]string, error)       { return f.localNames, nil }
func (f *fakePruner) IsMergedInto(name, _ string) (bool, error) { return f.mergedSet[name], nil }

func openTestBranchStore(t *testing.T) *store.Store {
	t.Helper()

	s, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	return s
}

func TestBranchList_json(t *testing.T) {
	t.Parallel()

	s := openTestBranchStore(t)
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "ABC-42", Title: "Add OAuth login", StatusID: 1},
		&store.Branch{UUID: "550e8400", Name: "ABC-42@feat@add-oauth-login@550e8400", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var buf bytes.Buffer
	if err := runList(t.Context(), &buf, s, listFlags{jsonOut: true}); err != nil {
		t.Fatalf("runBranchList: %v", err)
	}

	var rows []store.BranchRow
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].IssueSlug != "ABC-42" {
		t.Errorf("IssueSlug = %q, want ABC-42", rows[0].IssueSlug)
	}
	if rows[0].BranchName != "ABC-42@feat@add-oauth-login@550e8400" {
		t.Errorf("BranchName = %q", rows[0].BranchName)
	}
	if string(rows[0].Status) != "in_progress" {
		t.Errorf("Status = %q, want in_progress", rows[0].Status)
	}
}

func TestBranchList_json_emptyStore(t *testing.T) {
	t.Parallel()

	s := openTestBranchStore(t)

	var buf bytes.Buffer
	if err := runList(t.Context(), &buf, s, listFlags{jsonOut: true}); err != nil {
		t.Fatalf("runList: %v", err)
	}

	var rows []store.BranchRow
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

func TestBranchList_stdout(t *testing.T) {
	t.Parallel()

	s := openTestBranchStore(t)
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "XY-1", Title: "Some feature", StatusID: 1},
		&store.Branch{UUID: "aabbccdd", Name: "XY-1@feat@some-feature@aabbccdd", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var buf bytes.Buffer
	if err := runList(t.Context(), &buf, s, listFlags{stdout: true}); err != nil {
		t.Fatalf("runList: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "ISSUE ID") {
		t.Errorf("output missing ISSUE ID header: %q", out)
	}
	if !strings.Contains(out, "XY-1") {
		t.Errorf("output missing issue slug XY-1: %q", out)
	}
}

func TestBranchList_stdout_emptyStore(t *testing.T) {
	t.Parallel()

	s := openTestBranchStore(t)

	var buf bytes.Buffer
	if err := runList(t.Context(), &buf, s, listFlags{stdout: true}); err != nil {
		t.Fatalf("runList: %v", err)
	}

	if !strings.Contains(buf.String(), "No branches found.") {
		t.Errorf("expected 'No branches found.', got: %q", buf.String())
	}
}

// --- prune tests ---

func insertTestBranch(t *testing.T, s *store.Store, uuid, slug, name, btype string) {
	t.Helper()
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: slug, Title: slug, StatusID: 1},
		&store.Branch{UUID: uuid, Name: name, Type: btype, StatusID: 1},
	); err != nil {
		t.Fatalf("insert %s: %v", name, err)
	}
}

func TestRunBranchPrune_deletesGoneRef(t *testing.T) {
	t.Parallel()

	s := openTestBranchStore(t)
	insertTestBranch(t, s, "uuid-1", "ABC-1", "ABC-1@feat@gone@uuid-1", "feat")

	pruner := &fakePruner{base: "master", localNames: []string{"master"}}

	var buf bytes.Buffer
	if err := runPrune(t.Context(), &buf, s, pruner, pruneFlags{dryRun: true}); err != nil {
		t.Fatalf("runBranchPrune: %v", err)
	}
	if !strings.Contains(buf.String(), "ABC-1@feat@gone@uuid-1") {
		t.Errorf("expected deleted branch in output, got: %q", buf.String())
	}

	// dry-run must not mutate the store.
	rows, _ := s.ListBranches(t.Context(), store.BranchStatusAll)
	if len(rows) != 1 {
		t.Errorf("store should be unchanged after dry-run, got %d rows", len(rows))
	}
}

func TestRunBranchPrune_marksMerged(t *testing.T) {
	t.Parallel()

	s := openTestBranchStore(t)
	insertTestBranch(t, s, "uuid-2", "XY-1", "XY-1@fix@bug@uuid-2", "fix")

	pruner := &fakePruner{
		base:       "master",
		localNames: []string{"master", "XY-1@fix@bug@uuid-2"},
		mergedSet:  map[string]bool{"XY-1@fix@bug@uuid-2": true},
	}

	var buf bytes.Buffer
	if err := runPrune(t.Context(), &buf, s, pruner, pruneFlags{dryRun: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}
	if !strings.Contains(buf.String(), "XY-1@fix@bug@uuid-2") {
		t.Errorf("expected merged branch in output, got: %q", buf.String())
	}
}

func TestRunBranchPrune_nothingToPrune(t *testing.T) {
	t.Parallel()

	s := openTestBranchStore(t)
	insertTestBranch(t, s, "uuid-3", "Z-1", "Z-1@feat@active@uuid-3", "feat")

	pruner := &fakePruner{
		base:       "master",
		localNames: []string{"master", "Z-1@feat@active@uuid-3"},
		mergedSet:  map[string]bool{},
	}

	var buf bytes.Buffer
	if err := runPrune(t.Context(), &buf, s, pruner, pruneFlags{dryRun: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}
	if !strings.Contains(buf.String(), "Nothing to prune.") {
		t.Errorf("expected 'Nothing to prune.', got: %q", buf.String())
	}
}

func TestRunBranchPrune_mixedDryRun(t *testing.T) {
	t.Parallel()

	s := openTestBranchStore(t)
	insertTestBranch(t, s, "del-1", "DEL-1", "DEL-1@feat@gone@del-1", "feat")
	insertTestBranch(t, s, "mrg-1", "MRG-1", "MRG-1@fix@done@mrg-1", "fix")
	insertTestBranch(t, s, "act-1", "ACT-1", "ACT-1@feat@active@act-1", "feat")

	pruner := &fakePruner{
		base:       "master",
		localNames: []string{"master", "MRG-1@fix@done@mrg-1", "ACT-1@feat@active@act-1"},
		mergedSet:  map[string]bool{"MRG-1@fix@done@mrg-1": true},
	}

	var buf bytes.Buffer
	if err := runPrune(t.Context(), &buf, s, pruner, pruneFlags{dryRun: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "DEL-1@feat@gone@del-1") {
		t.Errorf("expected deleted branch in output, got: %q", out)
	}
	if !strings.Contains(out, "MRG-1@fix@done@mrg-1") {
		t.Errorf("expected merged branch in output, got: %q", out)
	}
	if strings.Contains(out, "ACT-1@feat@active@act-1") {
		t.Errorf("active branch must not appear in output, got: %q", out)
	}

	// dry-run: store unchanged.
	rows, _ := s.ListBranches(t.Context(), store.BranchStatusAll)
	if len(rows) != 3 {
		t.Errorf("store should be unchanged after dry-run, got %d rows", len(rows))
	}
}
