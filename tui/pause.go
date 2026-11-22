package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// pauseTTY reports whether w and stdin are terminals; pauseIn is where the
// enter is read. Variables for the tests.
var (
	pauseTTY = func(w io.Writer) bool {
		f, ok := w.(*os.File)

		return ok && term.IsTerminal(int(f.Fd())) && term.IsTerminal(int(os.Stdin.Fd()))
	}
	pauseIn io.Reader = os.Stdin
)

// Pause waits for enter after a message written to w that a full-screen
// program is about to cover. It does nothing while the terminal is split
// (the message stays in the output band; this asks the session, not the
// config: a terminal too small to split still pauses), nor when w or stdin
// is no terminal, so a test or a pipe never blocks.
func Pause(w io.Writer) {
	if screen.quiet() || !pauseTTY(w) {
		return
	}
	fmt.Fprint(w, "[enter] to continue ")
	_, _ = bufio.NewReader(pauseIn).ReadString('\n')
}
