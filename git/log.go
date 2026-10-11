package git

import (
	"context"
	"strings"
)

// logGraphArgs are the `git log` arguments of the commit graph of `git zf
// log`: every ref but git-zf's own, one line per commit.
var logGraphArgs = []string{
	"log", "--exclude=*zf/*", "--graph", "--oneline", "--all", "--decorate",
	"--pretty=tformat:%Cred%h%Creset -%C(yellow)%d%Creset %s %Cgreen%cd (%cr) %C(bold blue)<%an by %cn>%Creset",
	"--abbrev-commit", "--date=iso", "--date-order",
}

// PrintLog writes the commit graph to the client's output, colored when it is
// a terminal. extra are further `git log` arguments, such as a revision range
// or -n.
func (c *Client) PrintLog(ctx context.Context, extra ...string) error {
	return c.runInteractive(ctx, c.root, append(append(logGraphArgs, extra...), "--color=auto")...)
}

// LogGraph returns the colored lines of the commit graph, newest first. extra
// are as for PrintLog.
func (c *Client) LogGraph(ctx context.Context, extra ...string) ([]string, error) {
	args := append(append(logGraphArgs, extra...), "--color=always")
	out, err := c.gitCmd(ctx, args...).Output()
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
