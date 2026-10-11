package git

import (
	"context"
	"strings"
)

// PrintLog runs `git <args>`, a log command, writing to the client's output,
// colored when it is a terminal.
func (c *Client) PrintLog(ctx context.Context, args ...string) error {
	return c.runInteractive(ctx, c.root, append(args, "--color=auto")...)
}

// Log runs `git <args>`, a log command, and returns its colored lines.
func (c *Client) Log(ctx context.Context, args ...string) ([]string, error) {
	out, err := c.gitCmd(ctx, append(args, "--color=always")...).Output()
	if err != nil {
		return nil, gitStderr(err)
	}

	return strings.Split(strings.TrimRight(string(out), "\n"), "\n"), nil
}

// ShowCommit returns the colored patch of one commit with its stat and raw
// file list, for the dialog of `git zf log`.
func (c *Client) ShowCommit(ctx context.Context, hash string) (string, error) {
	out, err := c.gitCmd(ctx, "log", "-1", "--decorate", "--graph", "--stat", "--raw", "-p",
		"--ignore-space-change", "--color=always", hash).Output()
	if err != nil {
		return "", gitStderr(err)
	}

	return string(out), nil
}
