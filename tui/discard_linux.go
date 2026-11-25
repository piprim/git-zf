package tui

import (
	"os"

	"golang.org/x/sys/unix"
)

// DiscardInput drops the keys typed while a command had the terminal, so
// that the screen that follows does not take them as its own: an enter
// typed during a push would open the menu of the row under the cursor.
// Without a terminal on stdin it does nothing.
func DiscardInput() { flushInput(int(os.Stdin.Fd())) }

func flushInput(fd int) { _ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH) }
