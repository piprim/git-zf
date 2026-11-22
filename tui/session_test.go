package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// testSession is a session on a buffer, rows tall, whose run records the
// models it is given and returns them as they came.
type testSession struct {
	*session
	out  *bytes.Buffer
	rows int
	cols int
	tty  bool
	ran  []tea.Model
	// during runs inside run, with the model it was given.
	during func(tea.Model)
}

func newTestSession(split bool, rows int) *testSession {
	ts := &testSession{out: &bytes.Buffer{}, rows: rows, cols: 80, tty: true}
	ts.session = &session{
		split:    split,
		size:     50,
		out:      ts.out,
		isTTY:    func() bool { return ts.tty },
		termSize: func() (int, int, error) { return ts.cols, ts.rows, nil },
		run: func(m tea.Model, _ ...tea.ProgramOption) (tea.Model, error) {
			ts.ran = append(ts.ran, m)
			if ts.during != nil {
				ts.during(m)
			}

			return m, nil
		},
	}

	return ts
}

func (ts *testSession) screen(t *testing.T) tea.Model {
	t.Helper()
	m, err := ts.runScreen(context.Background(), &stubScreen{})
	if err != nil {
		t.Fatalf("runScreen: %v", err)
	}

	return m
}

func (ts *testSession) lastIsBand() bool {
	_, ok := ts.ran[len(ts.ran)-1].(*band)

	return ok
}

func TestSession(t *testing.T) {
	l24 := layout{rows: 24, cols: 80, top: 12}

	for name, ts := range map[string]*testSession{
		"split off runs on the alternate screen": newTestSession(false, 24),
		"one stream redirected":                  func() *testSession { ts := newTestSession(true, 24); ts.tty = false; return ts }(),
		"terminal too small":                     newTestSession(true, 18),
	} {
		t.Run(name, func(t *testing.T) {
			ts.screen(t)
			if ts.lastIsBand() || ts.out.Len() != 0 || ts.active != nil || ts.outputHeight() != 0 {
				t.Errorf("band=%v out=%q active=%v", ts.lastIsBand(), ts.out.String(), ts.active)
			}
		})
	}

	t.Run("first screen opens the split", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.screen(t)
		if want := l24.open() + l24.before() + l24.after(); ts.out.String() != want || !ts.lastIsBand() {
			t.Errorf("band=%v out=%q, want %q", ts.lastIsBand(), ts.out.String(), want)
		}
	})

	t.Run("returns the inner model", func(t *testing.T) {
		ts := newTestSession(true, 24)
		if _, ok := ts.screen(t).(*stubScreen); !ok {
			t.Error("the band was returned, not the screen")
		}
	})

	t.Run("second screen does not reopen", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.screen(t)
		ts.out.Reset()
		ts.screen(t)
		if want := l24.before() + l24.after(); ts.out.String() != want {
			t.Errorf("out=%q, want %q", ts.out.String(), want)
		}
	})

	t.Run("shrunk below the floor closes the split", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.screen(t)
		ts.rows = 18
		ts.screen(t)
		if !strings.HasSuffix(ts.out.String(), l24.closeSeq()) || ts.active != nil || ts.lastIsBand() {
			t.Errorf("band=%v active=%v out=%q", ts.lastIsBand(), ts.active, ts.out.String())
		}
	})

	t.Run("shrunk below the band while open sets no invalid region", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.during = func(m tea.Model) {
			ts.rows = 10
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
		}
		ts.screen(t)
		if got := ts.out.String(); strings.Contains(got, "\x1b[13;10r") || !strings.Contains(got, "\x1b8\x1b7\x1b[r") || ts.active != nil {
			t.Errorf("active=%v out=%q", ts.active, got)
		}
	})

	t.Run("resized between screens lays out again without erasing", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.screen(t)
		ts.out.Reset()
		ts.rows = 40
		ts.screen(t)
		l40 := layout{rows: 40, cols: 80, top: 20}
		if got := ts.out.String(); !strings.HasPrefix(got, "\x1b[r"+l40.open()) || strings.Contains(got, "\x1b[2J") {
			t.Errorf("out=%q", got)
		}
	})

	t.Run("resized while open is read back", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.during = func(m tea.Model) {
			ts.rows, ts.cols = 30, 100
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		}
		ts.screen(t)
		want := layout{rows: 30, cols: 100, top: 12}
		if !strings.HasSuffix(ts.out.String(), want.after()) || ts.outputHeight() != 18 {
			t.Errorf("height=%d out=%q", ts.outputHeight(), ts.out.String())
		}
	})

	t.Run("a band resized while open keeps its top for the next screen", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.during = func(m tea.Model) {
			ts.rows = 30
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
		}
		ts.screen(t)
		quiet := ts.quiet()
		ts.during = nil
		ts.out.Reset()
		ts.screen(t)
		want := layout{rows: 30, cols: 80, top: 12}
		if got := ts.out.String(); !quiet || got != want.before()+want.after() {
			t.Errorf("quiet=%v out=%q", quiet, got)
		}
	})

	t.Run("shrunk while open until the output band is too small closes the split", func(t *testing.T) {
		ts := newTestSession(true, 40)
		ts.during = func(m tea.Model) {
			ts.rows = 22 // a new split would fit, the band of 20 rows does not
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 22})
		}
		ts.screen(t)
		if got := ts.out.String(); ts.active != nil || strings.Contains(got, "\x1b[21;22r\x1b8") {
			t.Errorf("active=%v out=%q", ts.active, got)
		}
	})

	t.Run("resized during an action is not quiet", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.screen(t)
		ts.rows = 30
		if ts.quiet() {
			t.Error("quiet, though the next screen scrolls the output away")
		}
	})

	t.Run("OutputHeight is never 0 while split", func(t *testing.T) {
		ts := newTestSession(true, 40)
		ts.screen(t)
		ts.rows = 20
		if got := ts.outputHeight(); got != 1 {
			t.Errorf("outputHeight=%d, want 1", got)
		}
	})

	t.Run("the lock is free during run", func(t *testing.T) {
		ts := newTestSession(true, 24)
		var active bool
		ts.during = func(tea.Model) { active = ts.splitActive() } // deadlocks if run holds the lock
		ts.screen(t)
		if !active {
			t.Error("the split is not active while its screen runs")
		}
	})

	t.Run("OutputHeight", func(t *testing.T) {
		ts := newTestSession(true, 24)
		before := ts.outputHeight()
		ts.screen(t)
		if before != 0 || ts.outputHeight() != 12 {
			t.Errorf("before=%d after=%d, want 0 and 12", before, ts.outputHeight())
		}
	})

	t.Run("a screen that ends on a terminal too small closes the split", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.during = func(tea.Model) { ts.rows = 15 }
		ts.screen(t)
		if ts.active != nil || !strings.HasSuffix(ts.out.String(), l24.closeSeq()) {
			t.Errorf("active=%v out=%q", ts.active, ts.out.String())
		}
	})

	t.Run("OutputHeight follows the terminal during an action", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.screen(t)
		ts.rows = 30
		if got := ts.outputHeight(); got != 18 {
			t.Errorf("OutputHeight = %d, want 18", got)
		}
	})

	t.Run("a dying session opens nothing", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.dying = true
		if _, err := ts.runScreen(context.Background(), &stubScreen{}); err == nil || ts.out.Len() != 0 || len(ts.ran) != 0 {
			t.Errorf("err=%v out=%q ran=%d", err, ts.out.String(), len(ts.ran))
		}
	})

	t.Run("close without open writes nothing", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.close()
		if ts.out.Len() != 0 {
			t.Errorf("out=%q", ts.out.String())
		}
	})

	t.Run("close twice writes closeSeq once", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.screen(t)
		ts.out.Reset()
		ts.close()
		ts.close()
		if ts.out.String() != l24.closeSeq() {
			t.Errorf("out=%q, want %q", ts.out.String(), l24.closeSeq())
		}
	})

	t.Run("the open hook runs once", func(t *testing.T) {
		ts := newTestSession(true, 24)
		var n int
		ts.onOpen = func() { n++ }
		ts.screen(t)
		ts.screen(t)
		if n != 1 {
			t.Errorf("onOpen ran %d times", n)
		}
	})
}
