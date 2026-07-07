package commit

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/go-git/go-git/v6/plumbing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
)

func TestAssembleMessage(t *testing.T) {
	tests := []struct {
		name    string
		tmpl    string
		answers map[string]any
		want    string
	}{
		{
			name: "type with scope and subject",
			tmpl: "{{.type}}{{with .scope}}({{.}}){{end}}: {{.subject}}",
			answers: map[string]any{
				"type": "feat", "scope": "auth", "subject": "add login",
			},
			want: "feat(auth): add login",
		},
		{
			name: "empty scope omitted",
			tmpl: "{{.type}}{{with .scope}}({{.}}){{end}}: {{.subject}}",
			answers: map[string]any{
				"type": "fix", "scope": "", "subject": "fix nil panic",
			},
			want: "fix: fix nil panic",
		},
		{
			name: "trims leading and trailing whitespace from subject",
			tmpl: "{{.type}}: {{.subject}}",
			answers: map[string]any{
				"type": "docs", "subject": "  update readme  ",
			},
			want: "docs: update readme",
		},
		{
			name: "full conventional commit with body and footer",
			tmpl: "{{.type}}: {{.subject}}{{with .body}}\n\n{{.}}{{end}}{{with .footer}}\n\n{{.}}{{end}}",
			answers: map[string]any{
				"type":    "feat",
				"subject": "add oauth",
				"body":    "implements google oauth flow",
				"footer":  "BREAKING CHANGE: removes basic auth",
			},
			want: "feat: add oauth\n\nimplements google oauth flow\n\nBREAKING CHANGE: removes basic auth",
		},
		{
			name: "empty body and footer omitted",
			tmpl: "{{.type}}: {{.subject}}{{with .body}}\n\n{{.}}{{end}}{{with .footer}}\n\n{{.}}{{end}}",
			answers: map[string]any{
				"type": "chore", "subject": "update deps", "body": "", "footer": "",
			},
			want: "chore: update deps",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := assembleMessage(&buf, tt.tmpl, tt.answers); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := buf.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildAuthorList(t *testing.T) {
	t.Parallel()

	t.Run("deduplicates repeated entries", func(t *testing.T) {
		t.Parallel()

		all := []string{
			"Alice <alice@example.com>",
			"Bob <bob@example.com>",
			"Alice <alice@example.com>",
		}
		got := BuildAuthorList(all, "")
		want := []string{"Alice <alice@example.com>", "Bob <bob@example.com>"}
		if len(got) != len(want) {
			t.Fatalf("len: got %d, want %d — list: %v", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("[%d] got %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("returns entries in sorted order", func(t *testing.T) {
		t.Parallel()

		all := []string{
			"Zoe <zoe@example.com>",
			"Alice <alice@example.com>",
			"Mia <mia@example.com>",
		}
		got := BuildAuthorList(all, "")
		for i := 1; i < len(got); i++ {
			if got[i] < got[i-1] {
				t.Errorf("not sorted at [%d]: %q < %q", i, got[i], got[i-1])
			}
		}
	})

	t.Run("places current user first when present in history", func(t *testing.T) {
		t.Parallel()

		all := []string{
			"Alice <alice@example.com>",
			"Bob <bob@example.com>",
			"Current User <current@example.com>",
		}
		current := "Current User <current@example.com>"
		got := BuildAuthorList(all, current)

		if len(got) == 0 {
			t.Fatal("empty list")
		}
		if got[0] != current {
			t.Errorf("first entry: got %q, want %q", got[0], current)
		}
		for _, a := range got[1:] {
			if a == current {
				t.Errorf("current user duplicated in list: %v", got)
			}
		}
	})

	t.Run("prepends current user when not already in history", func(t *testing.T) {
		t.Parallel()

		all := []string{"Alice <alice@example.com>", "Bob <bob@example.com>"}
		current := "New User <new@example.com>"
		got := BuildAuthorList(all, current)
		if got[0] != current {
			t.Errorf("first entry: got %q, want %q", got[0], current)
		}
		if len(got) != 3 {
			t.Errorf("len: got %d, want 3 — list: %v", len(got), got)
		}
	})

	t.Run("returns empty slice for nil input with no current user", func(t *testing.T) {
		t.Parallel()

		got := BuildAuthorList(nil, "")
		if len(got) != 0 {
			t.Errorf("expected empty, got %v", got)
		}
		got2 := BuildAuthorList(nil, "Alice <alice@example.com>")
		if len(got2) != 1 || got2[0] != "Alice <alice@example.com>" {
			t.Errorf("expected [Alice], got %v", got2)
		}
	})

	t.Run("returns single-entry list when current is the only item", func(t *testing.T) {
		t.Parallel()

		got := BuildAuthorList([]string{"Alice <alice@example.com>"}, "Alice <alice@example.com>")
		if len(got) != 1 || got[0] != "Alice <alice@example.com>" {
			t.Errorf("expected [Alice], got %v", got)
		}
	})

	t.Run("deduplicates current user that appears multiple times in history", func(t *testing.T) {
		t.Parallel()

		got := BuildAuthorList(
			[]string{"Alice <alice@example.com>", "Alice <alice@example.com>"},
			"Alice <alice@example.com>",
		)
		if len(got) != 1 || got[0] != "Alice <alice@example.com>" {
			t.Errorf("expected [Alice] (len 1), got %v", got)
		}
	})
}

const testIssueID = "ABC-1"

func TestSetItemValue(t *testing.T) {
	t.Parallel()

	t.Run("sets value and returns true when name is found", func(t *testing.T) {
		t.Parallel()

		items := []config.CommitItem{
			{Name: "scope"},
			{Name: "subject"},
		}

		ok := setItemValue(items, "scope", testIssueID)
		if !ok {
			t.Fatal("setItemValue: returned false, want true")
		}
		if items[0].Value != testIssueID {
			t.Errorf("items[0].Value = %q, want %q", items[0].Value, testIssueID)
		}
	})

	t.Run("returns false and leaves slice unchanged when name is missing", func(t *testing.T) {
		t.Parallel()

		items := []config.CommitItem{
			{Name: "subject"},
		}

		ok := setItemValue(items, "scope", testIssueID)
		if ok {
			t.Error("setItemValue: returned true, want false")
		}
		if items[0].Name != "subject" || items[0].Value != "" {
			t.Errorf("items[0] mutated: got %+v", items[0])
		}
	})
}

func TestIsValidCommitType(t *testing.T) {
	t.Parallel()

	t.Run("returns true when type is in the list", func(t *testing.T) {
		t.Parallel()

		types := []config.CommitTypeOption{
			{Name: "feat"},
			{Name: "fix"},
		}
		if !isValidCommitType(types, "feat") {
			t.Error("isValidCommitType(feat) = false, want true")
		}
	})

	t.Run("returns false when type is not in the list", func(t *testing.T) {
		t.Parallel()

		types := []config.CommitTypeOption{{Name: "feat"}}
		if isValidCommitType(types, "wip") {
			t.Error("isValidCommitType(wip) = true, want false")
		}
	})

	t.Run("returns false for a nil type list", func(t *testing.T) {
		t.Parallel()

		if isValidCommitType(nil, "feat") {
			t.Error("isValidCommitType(nil, feat) = true, want false")
		}
	})
}

func assertNoInputMutation(t *testing.T, got, want []config.CommitItem) {
	t.Helper()

	for i := range got {
		// CommitItem is not comparable (Options is a slice), so compare the
		// Name and Value fields manually.
		if got[i].Name != want[i].Name || got[i].Value != want[i].Value {
			t.Errorf("input mutated at [%d]: got %+v, want %+v",
				i, got[i], want[i])
		}
	}
}

func assertFieldValue(t *testing.T, items []config.CommitItem, name, want string) {
	t.Helper()

	for _, item := range items {
		if item.Name != name {
			continue
		}
		if item.Value != want {
			t.Errorf("%s.Value = %q, want %q", name, item.Value, want)
		}

		return
	}

	t.Errorf("field %q not found in result", name)
}

// fakeHistoryStore is a test double for historyStore.
type fakeHistoryStore struct {
	rows    []store.CommandHistoryRow
	err     error
	inserts []map[string]any // captured InsertCommandHistory payloads
}

func (f *fakeHistoryStore) InsertCommandHistory(_ context.Context, _ string, payload any) error {
	if m, ok := payload.(map[string]any); ok {
		f.inserts = append(f.inserts, m)
	}

	return f.err
}

func (f *fakeHistoryStore) ListCommandHistory(_ context.Context, _ string, _ int) ([]store.CommandHistoryRow, error) {
	return f.rows, f.err
}

// stubFormRunner is a formRunner stub for use in tests.
type stubFormRunner struct{ wantHistoryVal bool }

func (s *stubFormRunner) WantHistory() bool { return s.wantHistoryVal }

// swapRunFormFn replaces runFormFn for the test and restores it in Cleanup.
func swapRunFormFn(t *testing.T, fn func(*huh.Form, string) (formRunner, error)) {
	t.Helper()

	orig := runFormFn
	runFormFn = fn
	t.Cleanup(func() { runFormFn = orig })
}

func TestApplyPayload(t *testing.T) {
	t.Parallel()

	t.Run("sets matching string fields and ignores unknown keys", func(t *testing.T) {
		t.Parallel()

		items := []config.CommitItem{
			{Name: "scope"},
			{Name: "subject"},
			{Name: "body"},
		}
		payload := map[string]any{
			"scope":   "auth",
			"subject": "add OAuth",
			"unknown": "ignored",
		}

		got := applyPayload(items, payload)

		assertFieldValue(t, got, "scope", "auth")
		assertFieldValue(t, got, "subject", "add OAuth")
		assertFieldValue(t, got, "body", "")
	})

	t.Run("does not mutate the input slice", func(t *testing.T) {
		t.Parallel()

		items := []config.CommitItem{{Name: "scope"}, {Name: "subject"}}
		original := slices.Clone(items)

		applyPayload(items, map[string]any{"scope": "changed"})

		assertNoInputMutation(t, items, original)
	})

	t.Run("ignores non-string payload values", func(t *testing.T) {
		t.Parallel()

		items := []config.CommitItem{{Name: "scope"}}

		got := applyPayload(items, map[string]any{"scope": 42})

		assertFieldValue(t, got, "scope", "")
	})
}

func TestHistoryLabel(t *testing.T) {
	t.Parallel()

	t.Run("formats commit message and timestamp", func(t *testing.T) {
		t.Parallel()

		row := store.CommandHistoryRow{
			ID:        1,
			Payload:   []byte(`{"type":"feat","scope":"auth","subject":"add OAuth"}`),
			CreatedAt: time.Date(2026, 5, 10, 14, 32, 0, 0, time.UTC),
		}
		tmpl := `{{.type}}{{with .scope}}({{.}}){{end}}: {{.subject}}`

		label, err := historyLabel(tmpl, row)

		if err != nil {
			t.Fatalf("historyLabel: %v", err)
		}
		if !strings.Contains(label, "feat(auth): add OAuth") {
			t.Errorf("label does not contain message: %q", label)
		}
		if !strings.Contains(label, "[2026-05-10 14:32]") {
			t.Errorf("label does not contain date: %q", label)
		}
	})

	t.Run("truncates subject when it exceeds the max label length", func(t *testing.T) {
		t.Parallel()

		longSubject := strings.Repeat("x", 80)
		row := store.CommandHistoryRow{
			ID:        2,
			Payload:   []byte(`{"type":"feat","subject":"` + longSubject + `"}`),
			CreatedAt: time.Date(2026, 5, 10, 14, 32, 0, 0, time.UTC),
		}

		label, err := historyLabel(`{{.type}}: {{.subject}}`, row)
		if err != nil {
			t.Fatalf("historyLabel: %v", err)
		}

		subject := strings.SplitN(label, "  [", 2)[0]
		if len(strings.TrimRight(subject, " ")) > maxLabelLen {
			t.Errorf("subject portion too long: %d > %d", len(strings.TrimRight(subject, " ")), maxLabelLen)
		}
	})
}

// minimalCfg returns a minimal AppConfig sufficient for FillOutForm integration tests.
func minimalCfg() *config.AppConfig {
	return &config.AppConfig{
		CommitTypes: []config.CommitTypeOption{{Name: "feat"}},
		CommitMessage: config.CommitMessageConfig{
			Template: "{{.type}}: {{.subject}}",
			Items:    []config.CommitItem{{Name: "subject", Form: "input", Value: "my change"}},
		},
	}
}

func TestFillOutForm(t *testing.T) {
	// Not parallel — subtests swap the global runFormFn.

	t.Run("saves history after successful form completion", func(t *testing.T) {
		swapRunFormFn(t, func(_ *huh.Form, _ string) (formRunner, error) {
			return &stubFormRunner{}, nil
		})

		hs := &fakeHistoryStore{}

		// AnyOptionSet() == true skips the options form group (cleaner test).
		_, _, err := FillOutForm(context.Background(), minimalCfg(), tui.CommitOption{All: true}, hs, nil, "")
		if err != nil {
			t.Fatalf("FillOutForm: %v", err)
		}

		if len(hs.inserts) != 1 {
			t.Fatalf("inserts: got %d, want 1", len(hs.inserts))
		}
		if hs.inserts[0]["subject"] != "my change" {
			t.Errorf("saved subject = %q, want %q", hs.inserts[0]["subject"], "my change")
		}
	})

	t.Run("propagates user abort from the main form", func(t *testing.T) {
		swapRunFormFn(t, func(_ *huh.Form, _ string) (formRunner, error) {
			return nil, huh.ErrUserAborted
		})

		_, _, err := FillOutForm(context.Background(), minimalCfg(), tui.CommitOption{All: true}, &fakeHistoryStore{}, nil, "")
		if !errors.Is(err, huh.ErrUserAborted) {
			t.Errorf("err = %v, want huh.ErrUserAborted", err)
		}
	})

	t.Run("preserves original answers when history is empty", func(t *testing.T) {
		call := 0
		swapRunFormFn(t, func(_ *huh.Form, _ string) (formRunner, error) {
			call++
			switch call {
			case 1:
				// Main form: user presses ctrl+r.
				return &stubFormRunner{wantHistoryVal: true}, nil
			case 2:
				// "No commit history yet." dialog: user dismisses it.
				return &stubFormRunner{}, nil
			default:
				// Re-opened main form: user submits.
				return &stubFormRunner{}, nil
			}
		})

		hs := &fakeHistoryStore{} // empty history → triggers errNoHistory path

		_, _, err := FillOutForm(context.Background(), minimalCfg(), tui.CommitOption{All: true}, hs, nil, "")
		if err != nil {
			t.Fatalf("FillOutForm: %v", err)
		}

		// Three runFormFn invocations: main form, dialog, main form again.
		if call != 3 {
			t.Errorf("runFormFn calls: got %d, want 3", call)
		}

		// The original answers ("my change" from minimalCfg) must have been preserved
		// across the empty-history detour, so the final submission saves them.
		if len(hs.inserts) != 1 {
			t.Fatalf("inserts: got %d, want 1", len(hs.inserts))
		}
		if hs.inserts[0]["subject"] != "my change" {
			t.Errorf("saved subject = %q, want %q (answers were lost on empty-history detour)",
				hs.inserts[0]["subject"], "my change")
		}
	})

	t.Run("exits flow when user aborts the history picker", func(t *testing.T) {
		call := 0
		swapRunFormFn(t, func(_ *huh.Form, _ string) (formRunner, error) {
			call++
			if call == 1 {
				// Main form: user presses ctrl+r.
				return &stubFormRunner{wantHistoryVal: true}, nil
			}

			// Picker form: user aborts.
			return nil, huh.ErrUserAborted
		})

		hs := &fakeHistoryStore{
			rows: []store.CommandHistoryRow{
				{ID: 1, Payload: []byte(`{"type":"feat","subject":"prev"}`), CreatedAt: time.Now()},
			},
		}

		_, _, err := FillOutForm(context.Background(), minimalCfg(), tui.CommitOption{All: true}, hs, nil, "")
		if !errors.Is(err, huh.ErrUserAborted) {
			t.Errorf("err = %v, want huh.ErrUserAborted", err)
		}
	})

	t.Run("prefill values appear in the rendered output", func(t *testing.T) {
		swapRunFormFn(t, func(_ *huh.Form, _ string) (formRunner, error) {
			return &stubFormRunner{}, nil
		})

		hs := &fakeHistoryStore{}
		prefill := map[string]any{
			"subject": "Squashed merge of abc1234 into def5678.",
			"type":    "feat",
		}

		msg, _, err := FillOutForm(context.Background(), minimalCfg(), tui.CommitOption{All: true}, hs, prefill, "")
		if err != nil {
			t.Fatalf("FillOutForm: %v", err)
		}

		got := string(msg)
		if !strings.Contains(got, "Squashed merge of abc1234 into def5678.") {
			t.Errorf("rendered message %q does not contain prefilled subject", got)
		}
		if !strings.HasPrefix(got, "feat") {
			t.Errorf("rendered message %q does not start with prefilled type \"feat\"", got)
		}

		if len(hs.inserts) != 1 {
			t.Fatalf("inserts: got %d, want 1", len(hs.inserts))
		}
		if hs.inserts[0]["subject"] != "Squashed merge of abc1234 into def5678." {
			t.Errorf("saved subject = %q", hs.inserts[0]["subject"])
		}
		if hs.inserts[0]["type"] != "feat" {
			t.Errorf("saved type = %q", hs.inserts[0]["type"])
		}
	})

	t.Run("forwards the panel string to the form runner", func(t *testing.T) {
		var gotPanel string
		swapRunFormFn(t, func(_ *huh.Form, panel string) (formRunner, error) {
			gotPanel = panel
			return &stubFormRunner{}, nil
		})

		hs := &fakeHistoryStore{}
		_, _, err := FillOutForm(context.Background(), minimalCfg(), tui.CommitOption{All: true}, hs, nil, "PANEL")
		if err != nil {
			t.Fatalf("FillOutForm: %v", err)
		}
		if gotPanel != "PANEL" {
			t.Errorf("panel forwarded = %q, want %q", gotPanel, "PANEL")
		}
	})
}

// testCloseInfo is the IssueCloseInfo used by the Closing rows below. The
// hashes are chosen so their 7-char abbreviations are the readable
// "abc1234" / "def5678", making testCloseInfo.message() deterministic:
// "Squash abc1234 into def5678.".
var testCloseInfo = &IssueCloseInfo{
	FromHash: plumbing.NewHash("abc1234000000000000000000000000000000000"),
	ToHash:   plumbing.NewHash("def5678000000000000000000000000000000000"),
	Strategy: MergeStrategySquash,
}

const (
	testIssueSubject = "Add OAuth login"
	testMergeMsg     = "Squash abc1234 into def5678."
)

func TestIssueHint_Prefill(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		items       []config.CommitItem
		refFormat   string
		closeFormat string
		hint        IssueHint
		want        map[string]any
	}{
		{
			name: "scope_present_uses_scope_and_footer",
			items: []config.CommitItem{
				{Name: "subject"}, {Name: "scope"}, {Name: "footer"},
			},
			hint: IssueHint{IssueID: testIssueID, BranchType: "fix"},
			want: map[string]any{
				"scope":  testIssueID,
				"type":   "fix",
				"footer": "Refs #" + testIssueID,
			},
		},
		{
			name: "footer_used_when_scope_missing",
			items: []config.CommitItem{
				{Name: "subject"}, {Name: "footer"},
			},
			hint: IssueHint{IssueID: testIssueID, BranchType: "fix"},
			want: map[string]any{
				"subject": "(" + testIssueID + "): ",
				"footer":  "Refs #" + testIssueID,
				"type":    "fix",
			},
		},
		{
			// Without a footer item the ref message moves into the subject.
			name:  "subject_gets_ref_when_only_subject_present",
			items: []config.CommitItem{{Name: "subject"}},
			hint:  IssueHint{IssueID: testIssueID, BranchType: "fix"},
			want: map[string]any{
				"subject": "Refs #" + testIssueID + " - ",
				"type":    "fix",
			},
		},
		{
			// scope carries only the bare id, so the ref message still needs
			// a home: with no footer it goes into the subject.
			name:  "subject_gets_ref_when_scope_present_but_no_footer",
			items: []config.CommitItem{{Name: "subject"}, {Name: "scope"}},
			hint:  IssueHint{IssueID: testIssueID, BranchType: "fix"},
			want: map[string]any{
				"scope":   testIssueID,
				"subject": "Refs #" + testIssueID + " - ",
				"type":    "fix",
			},
		},
		{
			name:  "body_gets_ref_when_only_body_present",
			items: []config.CommitItem{{Name: "body"}},
			hint:  IssueHint{IssueID: testIssueID, BranchType: "fix"},
			want: map[string]any{
				"body": "Refs #" + testIssueID,
				"type": "fix",
			},
		},
		{
			name:  "body_gets_subject_then_ref_when_only_body_present",
			items: []config.CommitItem{{Name: "body"}},
			hint:  IssueHint{IssueID: testIssueID, BranchType: "fix", IssueSubject: testIssueSubject},
			want: map[string]any{
				"body": "# " + testIssueSubject + "\n\nRefs #" + testIssueID,
				"type": "fix",
			},
		},
		{
			name:  "no_matching_field_only_type",
			items: nil,
			hint:  IssueHint{IssueID: testIssueID, BranchType: "fix"},
			want: map[string]any{
				"type": "fix",
			},
		},
		{
			name:  "empty_issue_id_only_type",
			items: []config.CommitItem{{Name: "scope"}},
			hint:  IssueHint{BranchType: "fix"},
			want: map[string]any{
				"type": "fix",
			},
		},
		{
			name:  "empty_branchtype_only_issue",
			items: []config.CommitItem{{Name: "scope"}},
			hint:  IssueHint{IssueID: testIssueID},
			want: map[string]any{
				"scope": testIssueID,
			},
		},
		{
			name:  "fully_empty_hint_empty_map",
			items: []config.CommitItem{{Name: "scope"}},
			hint:  IssueHint{},
			want:  map[string]any{},
		},
		// Closing != nil cases
		{
			name: "closing_footer_gets_merge_info_subject_gets_title",
			items: []config.CommitItem{
				{Name: "subject"}, {Name: "footer"},
			},
			hint: IssueHint{IssueID: testIssueID, BranchType: "fix", IssueSubject: testIssueSubject, Closing: testCloseInfo},
			want: map[string]any{
				"subject": "[close] " + testIssueSubject,
				"footer":  "Closes #" + testIssueID + " - " + testMergeMsg,
				"type":    "fix",
			},
		},
		{
			name: "closing_scope_and_footer_sets_both",
			items: []config.CommitItem{
				{Name: "subject"}, {Name: "scope"}, {Name: "footer"},
			},
			hint: IssueHint{IssueID: testIssueID, BranchType: "fix", IssueSubject: testIssueSubject, Closing: testCloseInfo},
			want: map[string]any{
				"scope":   testIssueID,
				"subject": "[close] " + testIssueSubject,
				"footer":  "Closes #" + testIssueID + " - " + testMergeMsg,
				"type":    "fix",
			},
		},
		{
			// Without a footer item the close ref moves into the subject.
			name: "closing_scope_only_no_footer",
			items: []config.CommitItem{
				{Name: "subject"}, {Name: "scope"},
			},
			hint: IssueHint{IssueID: testIssueID, BranchType: "fix", IssueSubject: testIssueSubject, Closing: testCloseInfo},
			want: map[string]any{
				"scope":   testIssueID,
				"subject": "Closes #" + testIssueID + " - " + testIssueSubject,
				"type":    "fix",
			},
		},
		{
			name:  "closing_subject_fallback_when_no_scope_or_footer",
			items: []config.CommitItem{{Name: "subject"}},
			hint:  IssueHint{IssueID: testIssueID, BranchType: "fix", IssueSubject: testIssueSubject, Closing: testCloseInfo},
			want: map[string]any{
				"subject": "Closes #" + testIssueID + " - " + testIssueSubject,
				"type":    "fix",
			},
		},
		{
			name:  "closing_body_gets_close_ref_when_only_body_present",
			items: []config.CommitItem{{Name: "body"}},
			hint:  IssueHint{IssueID: testIssueID, BranchType: "fix", IssueSubject: testIssueSubject, Closing: testCloseInfo},
			want: map[string]any{
				"body": "Closes #" + testIssueID + " - " + testMergeMsg,
				"type": "fix",
			},
		},
		{
			name:  "closing_no_matching_field_only_type",
			items: nil,
			hint:  IssueHint{IssueID: testIssueID, BranchType: "fix", IssueSubject: testIssueSubject, Closing: testCloseInfo},
			want: map[string]any{
				"type": "fix",
			},
		},
		// Custom format strings
		{
			name:      "custom_ref_format_used_for_footer",
			items:     []config.CommitItem{{Name: "subject"}, {Name: "footer"}},
			refFormat: "Refs: %s",
			hint:      IssueHint{IssueID: testIssueID, BranchType: "fix"},
			want: map[string]any{
				"subject": "(" + testIssueID + "): ",
				"footer":  "Refs: " + testIssueID,
				"type":    "fix",
			},
		},
		{
			name:  "body_gets_issue_subject_when_present",
			items: []config.CommitItem{{Name: "scope"}, {Name: "footer"}, {Name: "body"}},
			hint:  IssueHint{IssueID: testIssueID, BranchType: "fix", IssueSubject: testIssueSubject},
			want: map[string]any{
				"scope":  testIssueID,
				"footer": "Refs #" + testIssueID,
				"body":   "# " + testIssueSubject,
				"type":   "fix",
			},
		},
		{
			name:        "custom_close_format_used_for_footer",
			items:       []config.CommitItem{{Name: "subject"}, {Name: "footer"}},
			closeFormat: "Tests #%s",
			hint:        IssueHint{IssueID: testIssueID, BranchType: "fix", IssueSubject: testIssueSubject, Closing: testCloseInfo},
			want: map[string]any{
				"subject": "[close] " + testIssueSubject,
				"footer":  "Tests #" + testIssueID + " - " + testMergeMsg,
				"type":    "fix",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			msgCfg := config.CommitMessageConfig{
				Items:       tt.items,
				RefFormat:   tt.refFormat,
				CloseFormat: tt.closeFormat,
			}
			got := tt.hint.Prefill(msgCfg)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Prefill() = %v, want %v", got, tt.want)
			}
		})
	}
}
