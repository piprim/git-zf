package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/piprim/git-zf/issue"
)

// typeKeys feeds text to m one rune at a time.
func typeKeys(m tea.Model, text string) tea.Model {
	for _, r := range text {
		m, _ = m.Update(runeKey(r))
	}

	return m
}

func TestIssueTable_Esc(t *testing.T) {
	rows := []issue.Row{{IssueSlug: "A", Title: "Alpha"}, {IssueSlug: "B", Title: "Beta"}}
	newTable := func(t *testing.T) tea.Model {
		t.Helper()
		m, err := IssueTableModel(rows, "all", IssueActions{Start: true})
		if err != nil {
			t.Fatal(err)
		}

		return m
	}

	t.Run("esc at the top level closes the view", func(t *testing.T) {
		m, cmd := newTable(t).Update(keyEsc)
		if pick, _ := PickedIssue(m); !quitsCmd(cmd) || pick != IssuePickNone {
			t.Errorf("quits = %v, pick = %v", quitsCmd(cmd), pick)
		}
	})

	t.Run("esc clears an applied filter first", func(t *testing.T) {
		m := typeKeys(newTable(t), "/Beta")
		m, _ = m.Update(keyEnter)
		if strings.Contains(m.View(), "Alpha") {
			t.Fatalf("filter not applied: %q", m.View())
		}
		m, cmd := m.Update(keyEsc)
		if quitsCmd(cmd) || !strings.Contains(m.View(), "Alpha") {
			t.Errorf("first esc: quits = %v, view = %q", quitsCmd(cmd), m.View())
		}
		if _, cmd = m.Update(keyEsc); !quitsCmd(cmd) {
			t.Error("second esc did not close the view")
		}
	})

	t.Run("esc disarms a pending abandon", func(t *testing.T) {
		m, _ := newTable(t).Update(runeKey('a'))
		if !strings.Contains(m.View(), "a again abandons") {
			t.Fatalf("a did not arm: %q", m.View())
		}
		m, cmd := m.Update(keyEsc)
		if quitsCmd(cmd) || strings.Contains(m.View(), "a again abandons") {
			t.Errorf("esc: quits = %v, view = %q", quitsCmd(cmd), m.View())
		}
		m, cmd = m.Update(runeKey('a'))
		if pick, _ := PickedIssue(m); quitsCmd(cmd) || pick != IssuePickNone {
			t.Errorf("a after esc acted: quits = %v, pick = %v", quitsCmd(cmd), pick)
		}
	})

	t.Run("esc in the filter input still only leaves the input", func(t *testing.T) {
		m := typeKeys(newTable(t), "/Be")
		if _, cmd := m.Update(keyEsc); quitsCmd(cmd) {
			t.Error("esc in the filter input closed the view")
		}
	})
}

func TestBranchTable_Esc(t *testing.T) {
	rows := branchTestRows(time.Now())
	opts := BranchTableOptions{Types: []string{"feat", "fix"}, Remote: "origin"}

	t.Run("esc at the top level closes the view", func(t *testing.T) {
		m, cmd := BranchTableModel(rows, opts).Update(keyEsc)
		if pick, _ := PickedBranch(m); !quitsCmd(cmd) || pick != BranchPickNone {
			t.Errorf("quits = %v, pick = %v", quitsCmd(cmd), pick)
		}
	})

	t.Run("esc clears an applied filter first", func(t *testing.T) {
		m := typeKeys(BranchTableModel(rows, opts), "/spike")
		m, _ = m.Update(keyEnter)
		if strings.Contains(m.View(), "master") {
			t.Fatalf("filter not applied: %q", m.View())
		}
		m, cmd := m.Update(keyEsc)
		if quitsCmd(cmd) || !strings.Contains(m.View(), "master") {
			t.Errorf("first esc: quits = %v, view = %q", quitsCmd(cmd), m.View())
		}
		if _, cmd = m.Update(keyEsc); !quitsCmd(cmd) {
			t.Error("second esc did not close the view")
		}
	})

	for _, key := range []rune{'a', 'D'} {
		t.Run("esc disarms a pending "+string(key), func(t *testing.T) {
			m, _ := BranchTableModel(rows, opts).Update(runeKey(key))
			if !strings.Contains(m.View(), string(key)+" again") {
				t.Fatalf("%c did not arm: %q", key, m.View())
			}
			m, cmd := m.Update(keyEsc)
			if quitsCmd(cmd) || strings.Contains(m.View(), string(key)+" again") {
				t.Errorf("esc: quits = %v, view = %q", quitsCmd(cmd), m.View())
			}
			m, cmd = m.Update(runeKey(key))
			if pick, _ := PickedBranch(m); quitsCmd(cmd) || pick != BranchPickNone {
				t.Errorf("%c after esc acted: quits = %v, pick = %v", key, quitsCmd(cmd), pick)
			}
		})
	}

	t.Run("esc in the filter input still only leaves the input", func(t *testing.T) {
		m := typeKeys(BranchTableModel(rows, opts), "/sp")
		if _, cmd := m.Update(keyEsc); quitsCmd(cmd) {
			t.Error("esc in the filter input closed the view")
		}
	})
}

func TestLogModel_Esc(t *testing.T) {
	lines := []string{"* bbbbbbb - feat: second commit", "* aaaaaaa - chore: init"}

	t.Run("esc at the top level closes the view", func(t *testing.T) {
		if _, cmd := newLog(lines, nil).Update(keyEsc); !quitsCmd(cmd) {
			t.Error("esc did not close the log")
		}
	})

	t.Run("esc clears an applied filter first", func(t *testing.T) {
		m := typeKeys(newLog(lines, nil), "/init")
		m, _ = m.Update(keyEnter)
		if strings.Contains(m.View(), "second commit") {
			t.Fatalf("filter not applied: %q", m.View())
		}
		m, cmd := m.Update(keyEsc)
		if quitsCmd(cmd) || !strings.Contains(m.View(), "second commit") {
			t.Errorf("first esc: quits = %v, view = %q", quitsCmd(cmd), m.View())
		}
		if _, cmd = m.Update(keyEsc); !quitsCmd(cmd) {
			t.Error("second esc did not close the view")
		}
	})

	t.Run("esc in the filter input still only leaves the input", func(t *testing.T) {
		m := typeKeys(newLog(lines, nil), "/in")
		if _, cmd := m.Update(keyEsc); quitsCmd(cmd) {
			t.Error("esc in the filter input closed the view")
		}
	})
}
