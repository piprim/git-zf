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
		&pkg.IO{In: strings.NewReader(""), Out: &out, Err: &errW},
		"echo", t.TempDir(), "hello",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Errorf("stdout tee: want %q in %q", "hello", out.String())
	}
}

func TestRunInteractive_failedCommandReturnsError(t *testing.T) {
	t.Parallel()

	var out, errW bytes.Buffer
	err := pkg.RunInteractive(
		context.Background(),
		&pkg.IO{In: strings.NewReader(""), Out: &out, Err: &errW},
		"false", t.TempDir(),
	)
	if err == nil {
		t.Fatal("expected error from false, got nil")
	}
}
