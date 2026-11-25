package tui

import (
	"os"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDiscardInput(t *testing.T) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	defer master.Close()
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("unlock pty: %v", err)
	}
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("pty number: %v", err)
	}
	fd, err := unix.Open("/dev/pts/"+strconv.Itoa(n), unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Skipf("open pty: %v", err)
	}
	defer unix.Close(fd)

	typed := func() int {
		// A typed line reaches the terminal's queue a moment after the write.
		for range 50 {
			if pending, _ := unix.IoctlGetInt(fd, unix.TIOCINQ); pending > 0 {
				return pending
			}
			time.Sleep(10 * time.Millisecond)
		}

		return 0
	}

	t.Run("a typed line is dropped", func(t *testing.T) {
		if _, err := master.WriteString("\n"); err != nil {
			t.Fatal(err)
		}
		if typed() == 0 {
			t.Fatal("the typed line never reached the terminal")
		}
		flushInput(fd)
		if pending, _ := unix.IoctlGetInt(fd, unix.TIOCINQ); pending != 0 {
			t.Errorf("%d bytes still pending after the discard", pending)
		}
	})
	t.Run("not a terminal is ignored", func(t *testing.T) {
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		flushInput(int(f.Fd()))
	})
}
