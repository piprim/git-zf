package pkg

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// IO holds the standard streams used for interactive subprocess operations.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// RunInteractive runs cmd with args in dir.
// See [Cmd].
func RunInteractive(ctx context.Context, ioStreams *IO, cmd, dir string, args ...string) error {
	if err := Cmd(ctx, ioStreams, cmd, dir, args...).Run(); err != nil {
		return fmt.Errorf("%w", err)
	}

	return nil
}

// Cmd return a [cmd.CommandContext] running cmd with args in dir, wiring
// in/out/errW directly to the subprocess. When out/errW are *os.File (e.g.
// os.Stdout), the file descriptor is passed through so the subprocess sees a
// real TTY and can emit colors.
func Cmd(ctx context.Context, ioStreams *IO, cmd, dir string, args ...string) *exec.Cmd {
	lio := IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
	if ioStreams != nil {
		if ioStreams.In != nil {
			lio.In = ioStreams.In
		}
		if ioStreams.Out != nil {
			lio.Out = ioStreams.Out
		}
		if ioStreams.Err != nil {
			lio.Err = ioStreams.Err
		}
	}

	c := exec.CommandContext(ctx, cmd, args...)
	c.Dir = dir
	c.Stdin = lio.In
	c.Stdout = lio.Out
	c.Stderr = lio.Err

	return c
}
