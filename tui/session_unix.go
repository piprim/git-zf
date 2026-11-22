//go:build unix

package tui

import (
	"os"
	"os/signal"
	"syscall"
)

// watchSignals keeps the terminal sane when the process is interrupted or
// suspended between two screens, while a command has the terminal: a scroll
// region left behind would break the operator's shell. While a screen runs
// the terminal is raw and bubbletea handles what it gets.
func (s *session) watchSignals() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP,
		syscall.SIGTSTP, syscall.SIGCONT)
	go func() {
		for sig := range ch {
			s.onSignal(sig)
		}
	}()
}

// onSignal checks and acts under one lock: a screen must not open between
// the two.
func (s *session) onSignal(sig os.Signal) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch sig {
	case syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP:
		if s.inScreen {
			return
		}
		s.dying = true // nothing opens or waits for a key from here on
		s.closeLocked()
		s.raise(sig)
	case syscall.SIGTSTP:
		if s.inScreen {
			return
		}
		if s.active != nil {
			s.write("\x1b7\x1b[r\x1b8") // the shell gets the whole terminal
		}
		s.stop()
	case syscall.SIGCONT:
		if s.active != nil && !s.inScreen {
			s.write("\x1b7" + s.active.region() + "\x1b8")
		}
	}
}

// reraise lets sig do what it does to a process that does not catch it.
func reraise(sig os.Signal) {
	signal.Reset(sig)
	if n, ok := sig.(syscall.Signal); ok {
		_ = syscall.Kill(os.Getpid(), n)
	}
}

// stopSelf suspends the process with SIGSTOP, which nothing catches: the Go
// runtime keeps its own handler for SIGTSTP once it was asked for.
func stopSelf() { _ = syscall.Kill(os.Getpid(), syscall.SIGSTOP) }
