package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// session shares the terminal between git-zf's screens and the output of
// the commands they launch. Every screen starts through RunScreen: split,
// it is drawn inline in the top band (see band) while the terminal's scroll
// region keeps everything else under it; otherwise it takes the alternate
// screen.
type session struct {
	// mu guards active and inScreen. It is never held while a screen runs:
	// the signal goroutine needs it.
	mu    sync.Mutex
	split bool
	size  int

	out io.Writer
	// isTTY reports whether stdin, stdout and stderr are all terminals: the
	// screens draw on stdout, the commit form and the spinner on stderr.
	isTTY    func() bool
	termSize func() (cols, rows int, err error)
	run      func(m tea.Model, opts ...tea.ProgramOption) (tea.Model, error)
	// onOpen runs once, when the split first opens.
	onOpen func()
	opened bool

	// active is the layout the terminal is in; nil when no region is set.
	active   *layout
	inScreen bool
	// dying is set when a signal is ending the process.
	dying bool
	// raise lets a signal do what it does to a process that does not catch
	// it; stop suspends the process. Fields for the tests.
	raise func(os.Signal)
	stop  func()
}

func newSession() *session {
	s := &session{
		out: os.Stdout,
		isTTY: func() bool {
			for _, f := range []*os.File{os.Stdin, os.Stdout, os.Stderr} {
				if !term.IsTerminal(int(f.Fd())) {
					return false
				}
			}

			return true
		},
		termSize: func() (int, int, error) { return term.GetSize(int(os.Stdout.Fd())) },
		run: func(m tea.Model, opts ...tea.ProgramOption) (tea.Model, error) {
			return tea.NewProgram(m, opts...).Run()
		},
	}
	s.raise, s.stop = reraise, stopSelf
	s.onOpen = func() {
		// A pager would draw on the whole terminal from inside the band.
		_ = os.Setenv("GIT_PAGER", "cat")
		s.watchSignals()
	}

	return s
}

// screen is the session of the process.
var screen = newSession()

// Configure sets how the screens share the terminal, from the [ui] section.
func Configure(split bool, size int) {
	screen.mu.Lock()
	defer screen.mu.Unlock()
	screen.split, screen.size = split, size
}

// RunScreen runs a full-screen model, m, and returns it as it ended. Every
// screen of git-zf starts here.
func RunScreen(ctx context.Context, m tea.Model) (tea.Model, error) {
	return screen.runScreen(ctx, m)
}

// CloseScreen gives the terminal back as a shell expects it. It is safe to
// call when no screen ever ran, and more than once.
func CloseScreen() { screen.close() }

// SplitActive reports whether the terminal is split right now.
func SplitActive() bool { return screen.splitActive() }

// OutputHeight is the number of rows of the output band while the terminal
// is split, 0 otherwise.
func OutputHeight() int { return screen.outputHeight() }

func (s *session) write(seq string) { _, _ = io.WriteString(s.out, seq) }

// layoutNow is the layout the terminal allows at this moment.
func (s *session) layoutNow() (layout, bool) {
	if !s.split || !s.isTTY() {
		return layout{}, false
	}
	cols, rows, err := s.termSize()
	if err != nil {
		return layout{}, false
	}

	return newLayout(rows, cols, s.size)
}

// errDying is what a screen gets when a signal is already ending the
// process: nothing more is drawn on a terminal that was just given back.
var errDying = errors.New("interrupted")

func (s *session) runScreen(ctx context.Context, m tea.Model) (tea.Model, error) {
	s.mu.Lock()
	if s.dying {
		s.mu.Unlock()

		return m, errDying
	}
	l, ok := s.layoutNow()
	if !ok {
		s.closeLocked()
		s.mu.Unlock()

		return s.run(m, tea.WithContext(ctx), tea.WithAltScreen())
	}
	switch {
	case s.active == nil:
		s.write(l.open())
		if s.onOpen != nil && !s.opened {
			s.opened = true
			s.onOpen()
		}
	case s.kept():
		// A band resized while its screen was open keeps its top.
		l = *s.active
	default:
		// The terminal was resized since the last screen: what it shows,
		// the output included, is scrolled into the scrollback and the
		// bands are laid out again.
		s.write("\x1b[r" + l.open())
	}
	s.active = &l
	s.write(l.before())
	s.inScreen = true
	b := &band{inner: m, l: l, out: s.out}
	s.mu.Unlock()

	_, err := s.run(b, tea.WithContext(ctx))

	s.mu.Lock()
	defer s.mu.Unlock()
	s.inScreen = false
	if s.active != nil {
		*s.active = b.l // the band follows a resize while its screen is open
		// Resized while the screen was open so that a band lost its least
		// height: the action that follows gets the whole terminal, and its
		// errors their pause. The region of after() could be invalid there
		// (top+1 > rows).
		if s.kept() {
			s.write(s.active.after())
		} else {
			s.write("\x1b8")
			s.closeLocked()
		}
	}

	return b.inner, err
}

func (s *session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
}

// kept reports whether the active layout still describes the terminal: the
// same size, and room for both bands. Otherwise the next screen lays the
// terminal out again, or runs full screen. The caller holds the lock.
func (s *session) kept() bool {
	if s.active == nil {
		return false
	}
	l, ok := s.layoutNow()

	return ok && l.rows == s.active.rows && l.cols == s.active.cols && s.active.fits()
}

func (s *session) closeLocked() {
	if s.active != nil {
		s.write(s.active.closeSeq())
		s.active = nil
	}
}

// quiet reports whether a pause would be out of place: the message stays
// visible in the output band, or the process is ending. A terminal resized
// since the last screen does not count: the next screen will scroll the
// message away or be full screen, over it.
func (s *session) quiet() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.dying || s.kept()
}

func (s *session) splitActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.active != nil
}

func (s *session) outputHeight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return 0
	}
	rows := s.active.rows
	if _, now, err := s.termSize(); err == nil {
		rows = now // the terminal may have been resized since the screen closed
	}

	// Never 0 while split: 0 says the terminal is not.
	return max(1, rows-s.active.top)
}
