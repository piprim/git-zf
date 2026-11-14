package pkg

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedBuffer is a terminal stand-in the spinner's goroutine and the test
// can both touch.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.String()
}

func TestSpin(t *testing.T) {
	t.Parallel()

	t.Run("no-op when Err is not a terminal", func(t *testing.T) {
		t.Parallel()

		var out, errW bytes.Buffer
		s := &IO{Out: &out, Err: &errW}
		stop := s.Spin("Fetching")
		fmt.Fprintln(s.Err, "warning")
		stop()

		if got := errW.String(); got != "warning\n" {
			t.Errorf("Err: want only the warning, got %q", got)
		}
	})

	t.Run("no-op on a nil IO", func(t *testing.T) {
		t.Parallel()

		(*IO)(nil).Spin("Fetching")()
	})

	t.Run("a redirected stdout is left alone", func(t *testing.T) {
		t.Parallel()

		f, err := os.Create(filepath.Join(t.TempDir(), "out"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		var tty lockedBuffer
		s := &IO{Out: f, Err: &bytes.Buffer{}}
		stop := s.spinOn(&tty, "Fetching", time.Millisecond)
		defer stop()
		if s.Out != f {
			t.Error("Out was wrapped")
		}
	})

	t.Run("an unfinished line is not drawn over", func(t *testing.T) {
		t.Parallel()

		var tty lockedBuffer
		var errW bytes.Buffer
		s := &IO{Err: &errW}
		stop := s.spinOn(&tty, "Fetching", time.Millisecond)
		waitFor(t, &tty, "⠙ Fetching…")
		fmt.Fprint(s.Err, "part1 ")
		before := tty.String()
		time.Sleep(10 * time.Millisecond)
		during := tty.String()
		fmt.Fprintln(s.Err, "part2")
		stop()

		if during != before {
			t.Errorf("drawn while the line was unfinished: %q", strings.TrimPrefix(during, before))
		}
		if errW.String() != "part1 part2\n" {
			t.Errorf("Err: %q", errW.String())
		}
	})

	var tty lockedBuffer
	var out, errW bytes.Buffer
	s := &IO{Out: &out, Err: &errW}
	stop := s.spinOn(&tty, "Fetching", time.Millisecond)
	waitFor(t, &tty, "⠙ Fetching…")
	fmt.Fprintln(s.Err, "warning")
	fmt.Fprintln(s.Out, "done")
	stop()
	stop()
	drawn := tty.String()

	t.Run("draws frames with the message", func(t *testing.T) {
		if !strings.Contains(drawn, "⠋ Fetching…") || !strings.Contains(drawn, "⠙ Fetching…") {
			t.Errorf("want two frames in %q", drawn)
		}
	})

	t.Run("output written while spinning reaches its writer", func(t *testing.T) {
		if errW.String() != "warning\n" || out.String() != "done\n" {
			t.Errorf("Err %q, Out %q", errW.String(), out.String())
		}
	})

	t.Run("stop wipes the line and restores the writers", func(t *testing.T) {
		if !strings.HasSuffix(drawn, wipeLine) {
			t.Errorf("want the line wiped last, got %q", drawn)
		}
		if s.Out != &out || s.Err != &errW {
			t.Error("Out and Err were not restored")
		}
	})

	t.Run("nothing is drawn after stop", func(t *testing.T) {
		time.Sleep(5 * time.Millisecond)
		if tty.String() != drawn {
			t.Error("the spinner kept drawing")
		}
	})
}

// waitFor polls tty until it holds want: the spinner's goroutine may be
// scheduled late on a loaded machine.
func waitFor(t *testing.T, tty *lockedBuffer, want string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(tty.String(), want); {
		if time.Now().After(deadline) {
			t.Fatalf("%q never drawn", want)
		}
		time.Sleep(time.Millisecond)
	}
}
