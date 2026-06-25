package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PushKind classifies what a dry-run push to the remote would do for a branch.
type PushKind int

const (
	// PushUpToDate means the remote already has the branch tip (nothing to push).
	PushUpToDate PushKind = iota
	// PushNewBranch means the branch does not yet exist on the remote.
	PushNewBranch
	// PushFastForward means the remote branch would advance by fast-forward.
	PushFastForward
	// PushRejected means the push would be rejected (non-fast-forward divergence).
	PushRejected
)

// PushOutcome is the parsed result of a dry-run push for a single branch.
type PushOutcome struct {
	Kind    PushKind
	Summary string // friendly line for the preview, e.g. "abc1234..def5678" or "[new branch]"
}

// PushDryRun runs `git push --porcelain --dry-run <remote> <branch>:<branch>`
// (under LC_ALL=C) and parses the per-ref porcelain flag char into a PushOutcome.
// The remote is contacted for ref negotiation but no objects transfer, so the
// result reflects the true remote state. ok is false (caller skips the proposal)
// when no remote is configured, when the branch is up to date, or when the
// dry-run output cannot be parsed. A real dry-run command failure (e.g. remote
// unreachable) returns a wrapped error with ok=false.
func (c *Client) PushDryRun(ctx context.Context, branch string) (PushOutcome, bool, error) {
	remote, err := c.Remote()
	if err != nil {
		return PushOutcome{}, false, fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return PushOutcome{}, false, nil
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return PushOutcome{}, false, fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root,
		"push", "--porcelain", "--dry-run", remote, branch+":"+branch)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, runErr := cmd.CombinedOutput()

	// Parse regardless of exit code: a rejected (non-fast-forward) push exits 1
	// but still emits a porcelain status line we want to surface.
	outcome, parsed := parsePushPorcelain(string(out))
	if !parsed {
		if runErr != nil {
			return PushOutcome{}, false, fmt.Errorf("push --dry-run %s: %w (%s)", branch, runErr, strings.TrimSpace(string(out)))
		}
		return PushOutcome{}, false, nil
	}
	if outcome.Kind == PushUpToDate {
		return outcome, false, nil
	}
	return outcome, true, nil
}

// parsePushPorcelain reads the first ref status line of `git push --porcelain`
// output. The leading flag char is the contract: '=' up to date, ' ' (or empty)
// fast-forward, '*' new ref, '!' rejected, '+' forced. Header ("To ...") and
// trailer ("Done") lines are ignored. Returns parsed=false when no ref line is
// found.
func parsePushPorcelain(out string) (PushOutcome, bool) {
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, "To ") || strings.HasPrefix(line, "Done") {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 2 {
			continue
		}
		flag := fields[0]
		summary := ""
		if len(fields) == 3 {
			summary = fields[2]
		}
		switch flag {
		case "=":
			return PushOutcome{Kind: PushUpToDate, Summary: "up to date"}, true
		case "*":
			return PushOutcome{Kind: PushNewBranch, Summary: "[new branch]"}, true
		case "!":
			return PushOutcome{Kind: PushRejected, Summary: "rejected (non-fast-forward)"}, true
		case "", " ", "+":
			if summary == "" {
				summary = "fast-forward"
			}
			return PushOutcome{Kind: PushFastForward, Summary: summary}, true
		}
	}
	return PushOutcome{}, false
}

// PushBranch runs `git push <remote> <branch>:<branch>` through the interactive
// IO streams (live progress). Never uses --force. No-op when no remote.
func (c *Client) PushBranch(ctx context.Context, branch string) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "push", remote, branch+":"+branch); err != nil {
		return fmt.Errorf("push %s: %w", branch, err)
	}
	return nil
}
