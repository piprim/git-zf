package tui

import (
	"testing"
)

func TestBranchConflictPicker_returnsGroup(t *testing.T) {
	t.Parallel()

	var action string
	g := BranchConflictPicker("ABC-42@feat@x", &action)

	if g == nil {
		t.Fatal("BranchConflictPicker returned nil")
	}
}

func TestVariantLabelInput_returnsGroup(t *testing.T) {
	t.Parallel()

	var label string
	g := VariantLabelInput(&label)

	if g == nil {
		t.Fatal("VariantLabelInput returned nil")
	}
}
