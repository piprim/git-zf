package pkg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

// RunInteractive runs cmd with args in dir, wiring in/out/errW to the
// subprocess. Stdout and stderr are teed: output streams live to the caller's
// writers and is captured in a buffer included in any error returned.
func RunInteractive(ctx context.Context, in io.Reader, out, errW io.Writer, cmd, dir string, args ...string) error {
	var buf bytes.Buffer

	c := exec.CommandContext(ctx, cmd, args...)
	c.Dir = dir
	c.Stdin = in
	c.Stdout = io.MultiWriter(out, &buf)
	c.Stderr = io.MultiWriter(errW, &buf)

	if err := c.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, buf.String())
	}

	return nil
}

