package commit

import (
	"bytes"
	"slices"
	"testing"

	"github.com/piprim/git-zf/config"
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

func TestBuildAuthorList_deduplication(t *testing.T) {
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
}

func TestBuildAuthorList_sortOrder(t *testing.T) {
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
}

func TestBuildAuthorList_currentUserFirst(t *testing.T) {
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
}

func TestBuildAuthorList_currentUserNotInHistory(t *testing.T) {
	all := []string{"Alice <alice@example.com>", "Bob <bob@example.com>"}
	current := "New User <new@example.com>"
	got := BuildAuthorList(all, current)
	if got[0] != current {
		t.Errorf("first entry: got %q, want %q", got[0], current)
	}
	if len(got) != 3 {
		t.Errorf("len: got %d, want 3 — list: %v", len(got), got)
	}
}

func TestBuildAuthorList_nilInput(t *testing.T) {
	got := BuildAuthorList(nil, "")
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
	got2 := BuildAuthorList(nil, "Alice <alice@example.com>")
	if len(got2) != 1 || got2[0] != "Alice <alice@example.com>" {
		t.Errorf("expected [Alice], got %v", got2)
	}
}

func TestBuildAuthorList_currentIsOnlyEntry(t *testing.T) {
	got := BuildAuthorList([]string{"Alice <alice@example.com>"}, "Alice <alice@example.com>")
	if len(got) != 1 || got[0] != "Alice <alice@example.com>" {
		t.Errorf("expected [Alice], got %v", got)
	}
}

func TestBuildAuthorList_currentAppearsMultipleTimes(t *testing.T) {
	got := BuildAuthorList(
		[]string{"Alice <alice@example.com>", "Alice <alice@example.com>"},
		"Alice <alice@example.com>",
	)
	if len(got) != 1 || got[0] != "Alice <alice@example.com>" {
		t.Errorf("expected [Alice] (len 1), got %v", got)
	}
}

const testIssueID = "ABC-1"

func TestSetItemValue_found(t *testing.T) {
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
}

func TestSetItemValue_notFound(t *testing.T) {
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
}

func TestIsValidCommitType_match(t *testing.T) {
	types := []config.CommitTypeOption{
		{Name: "feat"},
		{Name: "fix"},
	}
	if !isValidCommitType(types, "feat") {
		t.Error("isValidCommitType(feat) = false, want true")
	}
}

func TestIsValidCommitType_noMatch(t *testing.T) {
	types := []config.CommitTypeOption{{Name: "feat"}}
	if isValidCommitType(types, "wip") {
		t.Error("isValidCommitType(wip) = true, want false")
	}
}

func TestIsValidCommitType_emptyTypes(t *testing.T) {
	if isValidCommitType(nil, "feat") {
		t.Error("isValidCommitType(nil, feat) = true, want false")
	}
}

func TestApplyIssueHint(t *testing.T) {
	tests := []struct {
		name      string
		items     []config.CommitItem
		hint      IssueHint
		wantField string // empty = no field set
		wantValue string
	}{
		{
			name: "scope_wins_over_footer_and_subject",
			items: []config.CommitItem{
				{Name: "subject"}, {Name: "scope"}, {Name: "footer"},
			},
			hint:      IssueHint{IssueID: testIssueID},
			wantField: "scope",
			wantValue: testIssueID,
		},
		{
			name: "footer_used_when_scope_missing",
			items: []config.CommitItem{
				{Name: "subject"}, {Name: "footer"},
			},
			hint:      IssueHint{IssueID: testIssueID},
			wantField: "footer",
			wantValue: "Refs: " + testIssueID,
		},
		{
			name:      "subject_used_when_only_subject_present",
			items:     []config.CommitItem{{Name: "subject"}},
			hint:      IssueHint{IssueID: testIssueID},
			wantField: "subject",
			wantValue: "(" + testIssueID + ")",
		},
		{
			name:      "no_matching_field_no_change",
			items:     []config.CommitItem{{Name: "body"}},
			hint:      IssueHint{IssueID: testIssueID},
			wantField: "",
		},
		{
			name:      "empty_issue_id_early_return",
			items:     []config.CommitItem{{Name: "scope"}, {Name: "footer"}},
			hint:      IssueHint{IssueID: ""},
			wantField: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Snapshot original to ensure applyIssueHint never mutates the input.
			originalCopy := slices.Clone(tt.items)

			got := applyIssueHint(tt.items, tt.hint)

			assertNoInputMutation(t, tt.items, originalCopy)

			if tt.wantField == "" {
				assertNoFieldSet(t, got)

				return
			}

			assertFieldValue(t, got, tt.wantField, tt.wantValue)
		})
	}
}

func assertNoInputMutation(t *testing.T, got, want []config.CommitItem) {
	t.Helper()

	for i := range got {
		// CommitItem is not comparable (Options is a slice), so compare the
		// fields applyIssueHint could touch.
		if got[i].Name != want[i].Name || got[i].Value != want[i].Value {
			t.Errorf("input mutated at [%d]: got %+v, want %+v",
				i, got[i], want[i])
		}
	}
}

func assertNoFieldSet(t *testing.T, items []config.CommitItem) {
	t.Helper()

	for _, item := range items {
		if item.Value != "" {
			t.Errorf("expected no field set, but %q = %q", item.Name, item.Value)
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
