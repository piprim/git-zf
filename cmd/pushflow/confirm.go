package pushflow

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/tui"
)

// NewHuhConfirm returns the production ConfirmFunc: a huh confirm whose default
// selection is Yes (the push is the expected outcome of an opt-in flow).
func NewHuhConfirm() ConfirmFunc {
	return func(ctx context.Context, summary string) (bool, error) {
		confirmed := true // default Yes
		if err := huh.NewForm(tui.PushConfirm(summary, &confirmed)).RunWithContext(ctx); err != nil {
			return false, fmt.Errorf("push confirm form: %w", err)
		}
		return confirmed, nil
	}
}
