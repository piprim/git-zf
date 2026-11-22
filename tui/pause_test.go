package tui

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestPause(t *testing.T) {
	// onTerminal makes Pause see a terminal with one enter typed, and sess
	// the session in charge.
	onTerminal := func(t *testing.T, sess *session) {
		t.Helper()
		oldTTY, oldIn, oldScreen := pauseTTY, pauseIn, screen
		pauseTTY, pauseIn, screen = func(io.Writer) bool { return true }, strings.NewReader("\n"), sess
		t.Cleanup(func() { pauseTTY, pauseIn, screen = oldTTY, oldIn, oldScreen })
	}

	t.Run("a writer that is no terminal is left alone and never blocks", func(t *testing.T) {
		var b bytes.Buffer
		Pause(&b)
		if b.Len() != 0 {
			t.Errorf("wrote %q, want nothing", b.String())
		}
	})

	t.Run("asks for enter before a full screen covers the message", func(t *testing.T) {
		onTerminal(t, newTestSession(false, 24).session)
		var b bytes.Buffer
		Pause(&b)
		if !strings.Contains(b.String(), "[enter]") {
			t.Errorf("wrote %q", b.String())
		}
	})

	t.Run("silent while the split is active", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.screen(t)
		onTerminal(t, ts.session)
		var b bytes.Buffer
		Pause(&b)
		if b.Len() != 0 {
			t.Errorf("wrote %q, want nothing", b.String())
		}
	})

	t.Run("silent when a signal is ending the process", func(t *testing.T) {
		ts := newTestSession(false, 24)
		ts.dying = true
		onTerminal(t, ts.session)
		var b bytes.Buffer
		Pause(&b)
		if b.Len() != 0 {
			t.Errorf("wrote %q, want nothing", b.String())
		}
	})

	t.Run("pauses when the terminal shrank under the floor during the action", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.screen(t)
		ts.rows = 15 // the next screen will be full screen and cover the message
		onTerminal(t, ts.session)
		var b bytes.Buffer
		Pause(&b)
		if !strings.Contains(b.String(), "[enter]") {
			t.Errorf("wrote %q", b.String())
		}
	})

	t.Run("still pauses once the split fell back", func(t *testing.T) {
		ts := newTestSession(true, 24)
		ts.screen(t)
		ts.rows = 18
		ts.screen(t)
		onTerminal(t, ts.session)
		var b bytes.Buffer
		Pause(&b)
		if !strings.Contains(b.String(), "[enter]") {
			t.Errorf("wrote %q", b.String())
		}
	})
}
