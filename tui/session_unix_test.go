//go:build unix

package tui

import (
	"os"
	"syscall"
	"testing"
)

func TestSession_Signals(t *testing.T) {
	l24 := layout{rows: 24, cols: 80, top: 12}
	// opened is a split session between two screens, its output emptied,
	// that records the signals it re-raises and whether it stopped itself.
	opened := func(t *testing.T) (ts *testSession, raised *[]os.Signal, stopped *bool) {
		t.Helper()
		ts = newTestSession(true, 24)
		ts.screen(t)
		ts.out.Reset()
		raised, stopped = new([]os.Signal), new(bool)
		ts.raise = func(sig os.Signal) { *raised = append(*raised, sig) }
		ts.stop = func() { *stopped = true }

		return ts, raised, stopped
	}

	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP} {
		t.Run(sig.String()+" between screens gives the terminal back and lets the signal through", func(t *testing.T) {
			ts, raised, _ := opened(t)
			ts.onSignal(sig)
			if ts.out.String() != l24.closeSeq() || !ts.dying || len(*raised) != 1 || (*raised)[0] != sig {
				t.Errorf("out=%q dying=%v raised=%v", ts.out.String(), ts.dying, *raised)
			}
		})
	}

	t.Run("an interrupt while a screen runs is bubbletea's", func(t *testing.T) {
		ts, raised, _ := opened(t)
		ts.inScreen = true
		ts.onSignal(syscall.SIGINT)
		if ts.out.Len() != 0 || ts.dying || len(*raised) != 0 || ts.active == nil {
			t.Errorf("out=%q dying=%v raised=%v", ts.out.String(), ts.dying, *raised)
		}
	})

	t.Run("a stop between screens hands the shell the whole terminal", func(t *testing.T) {
		ts, _, stopped := opened(t)
		ts.onSignal(syscall.SIGTSTP)
		if ts.out.String() != "\x1b7\x1b[r\x1b8" || !*stopped || ts.active == nil {
			t.Errorf("out=%q stopped=%v", ts.out.String(), *stopped)
		}
	})

	t.Run("a stop while a screen runs is ignored", func(t *testing.T) {
		ts, _, stopped := opened(t)
		ts.inScreen = true
		ts.onSignal(syscall.SIGTSTP)
		if ts.out.Len() != 0 || *stopped {
			t.Errorf("out=%q stopped=%v", ts.out.String(), *stopped)
		}
	})

	t.Run("a continue sets the region again", func(t *testing.T) {
		ts, _, _ := opened(t)
		ts.onSignal(syscall.SIGCONT)
		if ts.out.String() != "\x1b7\x1b[13;24r\x1b8" {
			t.Errorf("out=%q", ts.out.String())
		}
	})

	t.Run("a continue with no split writes nothing", func(t *testing.T) {
		ts := newTestSession(false, 24)
		ts.onSignal(syscall.SIGCONT)
		if ts.out.Len() != 0 {
			t.Errorf("out=%q", ts.out.String())
		}
	})
}
