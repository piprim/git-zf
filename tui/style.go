package tui

import "github.com/charmbracelet/lipgloss"

// HintStyle renders the actionable "Run 'cd …'" hints printed when a command
// leaves the user's shell somewhere else than where the work is (after
// `issue start` creates a worktree, or after a close removes the worktree the
// user was standing in). One definition so both hints look identical.
var HintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))
