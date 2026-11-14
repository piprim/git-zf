package pkg

import (
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

// spinInterval is the time between two frames of the spinner.
const spinInterval = 100 * time.Millisecond

var spinFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// wipeLine returns the cursor to the first column and erases the line.
const wipeLine = "\r\x1b[K"

// Spin shows a spinner followed by msg on Err until the returned stop is
// called (more than once is fine). It is a no-op when Err is not a terminal.
//
// While it spins, Out and Err are replaced by writers that wipe the spinner's
// line before they write, so a warning or the output of a git subprocess
// lands on a line of its own above it. Stop before a form or anything else
// that draws on the terminal without going through s. A writer read from s
// before a Spin or a stop is stale after it: read s.Err when writing.
func (s *IO) Spin(msg string) (stop func()) {
	if s == nil {
		return func() {}
	}
	f, ok := s.Err.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return func() {}
	}

	return s.spinOn(f, msg, spinInterval)
}

// spinOn is Spin on tty, whatever Err is.
func (s *IO) spinOn(tty io.Writer, msg string, every time.Duration) (stop func()) {
	line := &spinLine{tty: tty}
	out, errW := s.Out, s.Err
	s.Err = &lineWiper{line: line, w: errW}
	// A stdout that is redirected does not share the spinner's line.
	if f, ok := out.(*os.File); !ok || term.IsTerminal(int(f.Fd())) {
		if out != nil {
			s.Out = &lineWiper{line: line, w: out}
		}
	}

	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(every)
		defer tick.Stop()
		for i := 0; ; i++ {
			line.mu.Lock()
			if !line.partial {
				_, _ = io.WriteString(tty, "\r"+string(spinFrames[i%len(spinFrames)])+" "+msg+"…\x1b[K")
			}
			line.mu.Unlock()
			select {
			case <-done:
				return
			case <-tick.C:
			}
		}
	}()

	return sync.OnceFunc(func() {
		close(done)
		<-stopped
		if !line.partial {
			_, _ = io.WriteString(tty, wipeLine)
		}
		s.Out, s.Err = out, errW
	})
}

// spinLine is the terminal line the spinner and the wrapped writers share.
type spinLine struct {
	mu  sync.Mutex
	tty io.Writer
	// partial is true while the last write left its line unfinished (a
	// subprocess's output arrives in chunks): the spinner waits for the
	// newline instead of drawing over it.
	partial bool
}

// lineWiper writes to w after wiping the spinner's line; the next frame
// redraws the spinner under what was written.
type lineWiper struct {
	line *spinLine
	w    io.Writer
}

func (l *lineWiper) Write(p []byte) (int, error) {
	l.line.mu.Lock()
	defer l.line.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if !l.line.partial {
		_, _ = io.WriteString(l.line.tty, wipeLine)
	}
	l.line.partial = p[len(p)-1] != '\n'

	return l.w.Write(p) //nolint:wrapcheck // a writer in front of another
}
