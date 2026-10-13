package tui

import (
	"slices"
	"testing"

	"github.com/charmbracelet/huh"
)

func TestIssueLabelFields(t *testing.T) {
	t.Parallel()

	t.Run("single is one status pick, the current status read back", func(t *testing.T) {
		t.Parallel()

		fields, labels := IssueLabelFields([]string{"New", "Resolved"}, []string{"Resolved"}, true)
		if len(fields) != 1 {
			t.Fatalf("fields = %d, want 1", len(fields))
		}
		if _, ok := fields[0].(*huh.Select[string]); !ok {
			t.Errorf("field = %T, want a select", fields[0])
		}
		if got := labels(); !slices.Equal(got, []string{"Resolved"}) {
			t.Errorf("labels = %v", got)
		}
	})

	t.Run("no known label is the new-labels line alone, reading back nothing", func(t *testing.T) {
		t.Parallel()

		fields, labels := IssueLabelFields(nil, nil, false)
		if len(fields) != 1 {
			t.Fatalf("fields = %d, want 1", len(fields))
		}
		if _, ok := fields[0].(*huh.Input); !ok {
			t.Errorf("field = %T, want an input", fields[0])
		}
		if got := labels(); len(got) != 0 {
			t.Errorf("labels = %v, want none", got)
		}
	})

	t.Run("the choices and the current labels make one list, the current read back", func(t *testing.T) {
		t.Parallel()

		fields, labels := IssueLabelFields([]string{"bug", "ui"}, []string{"ui", "local"}, false)
		if len(fields) != 2 {
			t.Fatalf("fields = %d, want the list and the line", len(fields))
		}
		if _, ok := fields[0].(*huh.MultiSelect[string]); !ok {
			t.Errorf("field = %T, want a multi-select", fields[0])
		}
		if got := labels(); !slices.Equal(got, []string{"ui", "local"}) {
			t.Errorf("labels = %v", got)
		}
	})
}
