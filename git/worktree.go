package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CreateWorktree creates a new branch from baseBranch and checks it out
// in a linked worktree at path. Wraps `git worktree add -b <branch> <path> <base>`.
func (c *Client) CreateWorktree(ctx context.Context, branchName, baseBranch, path string) error {
	root := c.root

	if err := c.runInteractive(ctx, root, "worktree", "add", "-b", branchName, path, baseBranch); err != nil {
		return fmt.Errorf("create worktree %q: %w", path, err)
	}

	return nil
}

// Worktree is one entry of `git worktree list --porcelain`.
type Worktree struct {
	Path     string // absolute path of the working tree
	Branch   string // short branch name; "" when HEAD is detached
	Main     bool   // the main working tree (always the first entry)
	Prunable bool   // git flagged the entry prunable (its directory is gone)
}

// parseWorktreeList parses `git worktree list --porcelain` output. Entries are
// blank-line separated; the first entry is the main working tree.
func parseWorktreeList(out string) []Worktree {
	var (
		list []Worktree
		cur  *Worktree
	)

	flush := func() {
		if cur != nil {
			list = append(list, *cur)
			cur = nil
		}
	}

	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &Worktree{Path: strings.TrimPrefix(line, "worktree "), Main: len(list) == 0}
		case cur == nil:
			// attribute line before any "worktree" header: ignore
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case strings.HasPrefix(line, "prunable"):
			cur.Prunable = true
		}
	}
	flush()

	return list
}

// Worktrees lists every working tree of the repository (main first).
func (c *Client) Worktrees(ctx context.Context) ([]Worktree, error) {
	cmd := c.gitCmd(ctx, "worktree", "list", "--porcelain")
	cmd.Env = append(os.Environ(), "LC_ALL=C")

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}

	return parseWorktreeList(string(out)), nil
}

// WorktreeFor returns the linked worktree that has branch checked out, or nil
// when the branch is not checked out in any linked worktree. The main working
// tree is never returned. A returned entry may be Prunable (its directory is
// gone but git still records it); callers decide how to treat that.
func (c *Client) WorktreeFor(ctx context.Context, branch string) (*Worktree, error) {
	list, err := c.Worktrees(ctx)
	if err != nil {
		return nil, err
	}

	for i := range list {
		if !list[i].Main && list[i].Branch == branch {
			return &list[i], nil
		}
	}

	return nil, nil //nolint:nilnil // nil,nil is the documented "not in a worktree" answer
}

// HoldingWorktree returns the working tree — main or linked — that has branch
// checked out, or nil when no tree holds it. Unlike WorktreeFor it includes
// the main working tree, for callers that themselves run from a linked
// worktree and need to know where a branch lives regardless of which tree it
// is. A returned entry may be Prunable.
func (c *Client) HoldingWorktree(ctx context.Context, branch string) (*Worktree, error) {
	list, err := c.Worktrees(ctx)
	if err != nil {
		return nil, err
	}

	for i := range list {
		if list[i].Branch == branch {
			return &list[i], nil
		}
	}

	return nil, nil //nolint:nilnil // nil,nil is the documented "not checked out anywhere" answer
}

// RemoveWorktree runs `git worktree remove <path>` without --force, so git's
// own safety checks (modified or untracked files) apply and the error carries
// git's reason.
func (c *Client) RemoveWorktree(ctx context.Context, path string) error {
	cmd := c.gitCmd(ctx, "worktree", "remove", path)
	cmd.Env = append(os.Environ(), "LC_ALL=C")

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("worktree remove %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// CommonDir returns the absolute path of the common .git directory shared by
// every worktree. In the main tree it equals GitDir; in a linked worktree
// GitDir is .git/worktrees/<name> while CommonDir is the main .git.
func (c *Client) CommonDir() (string, error) {
	return c.revParsePath("--git-common-dir")
}

// MainTree returns a client anchored on the main working tree. When c already
// is the main tree it returns c. Otherwise it opens a new client at the main
// tree path and carries over the pinned remote. Close and other target-side
// flows use it so checkouts of the base branch and branch deletions run in the
// tree that holds the base, even when the command was typed inside a linked
// worktree.
func (c *Client) MainTree() (*Client, error) {
	root := c.root

	list, err := c.Worktrees(context.Background())
	if err != nil {
		return nil, err
	}

	for _, w := range list {
		if !w.Main {
			continue
		}
		if SamePath(w.Path, root) {
			return c, nil
		}

		m, err := NewClientAt(c.io, w.Path)
		if err != nil {
			return nil, err
		}
		m.remote, m.remoteResolved = c.remote, c.remoteResolved

		return m, nil
	}

	return c, nil
}

// SamePath reports whether a and b name the same directory once symlinks are
// resolved (t.TempDir on macOS and go-git roots may differ only by symlinks).
func SamePath(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = filepath.Clean(a)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = filepath.Clean(b)
	}

	return ra == rb
}
