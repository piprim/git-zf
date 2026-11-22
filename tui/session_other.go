//go:build !unix

package tui

import "os"

func (s *session) watchSignals() {}

func reraise(os.Signal) {}

func stopSelf() {}
