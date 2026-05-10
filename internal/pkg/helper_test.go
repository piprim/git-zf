package pkg_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/piprim/git-zf/internal/pkg"
)

func TestRunInteractive_outputTeed(t *testing.T) {
	t.Parallel()

	var out, errW bytes.Buffer
	err := pkg.RunInteractive(
		context.Background(),
		strings.NewReader(""),
		&out, &errW,
		"echo", t.TempDir(), "hello",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Errorf("stdout tee: want %q in %q", "hello", out.String())
	}
}

func TestRunInteractive_errorIncludesOutput(t *testing.T) {
	t.Parallel()

	var out, errW bytes.Buffer
	err := pkg.RunInteractive(
		context.Background(),
		strings.NewReader(""),
		&out, &errW,
		"false", t.TempDir(),
	)
	if err == nil {
		t.Fatal("expected error from false, got nil")
	}
}
