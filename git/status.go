package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// StatusEntry is one parsed line of `git status --porcelain=v2`.
type StatusEntry struct {
	// XY is the two-character status code with porcelain-v2 unmodified dots
	// normalised to spaces (e.g. " M", "M ", "MM", "A ", "??"). X is the
	// index/staged side, Y the worktree side. Untracked entries use "??";
	// unmerged entries keep their raw two letters (e.g. "UU").
	XY string
	// Path is the current path of the entry.
	Path string
	// OrigPath is the pre-rename/-copy path for '2' entries, else "".
	OrigPath string
}

// StatusEntries returns the working-tree status parsed from
// `git -C <root> status --porcelain=v2`. It shells out to the system git
// binary (like IsDirty) so semantics match git exactly.
func (c *Client) StatusEntries(ctx context.Context) ([]StatusEntry, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain=v2")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}

	return parsePorcelainV2(string(out)), nil
}

// parsePorcelainV2 parses porcelain=v2 output into entries. Ignored ('!') and
// header ('#') lines are dropped; unrecognised or malformed lines are skipped
// defensively so a single odd line never fails the whole status.
func parsePorcelainV2(raw string) []StatusEntry {
	var entries []StatusEntry

	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}

		switch line[0] {
		case '1': // ordinary: 1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>
			f := strings.SplitN(line, " ", 9)
			if len(f) < 9 {
				continue
			}
			entries = append(entries, StatusEntry{XY: normalizeXY(f[1]), Path: f[8]})
		case '2': // rename/copy: ... <Xscore> <path>\t<origPath>
			f := strings.SplitN(line, " ", 10)
			if len(f) < 10 {
				continue
			}
			path, orig, ok := strings.Cut(f[9], "\t")
			if !ok {
				continue
			}
			entries = append(entries, StatusEntry{XY: normalizeXY(f[1]), Path: path, OrigPath: orig})
		case 'u': // unmerged: u <XY> <sub> <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path>
			f := strings.SplitN(line, " ", 11)
			if len(f) < 11 {
				continue
			}
			entries = append(entries, StatusEntry{XY: normalizeXY(f[1]), Path: f[10]})
		case '?': // untracked: ? <path>
			entries = append(entries, StatusEntry{XY: "??", Path: strings.TrimPrefix(line, "? ")})
		default:
			// '!' ignored, '#' header, or anything unexpected → skip.
		}
	}

	return entries
}

// normalizeXY converts porcelain-v2 unmodified dots to spaces so the two-char
// code matches short-format conventions (" M" not ".M").
func normalizeXY(xy string) string {
	return strings.ReplaceAll(xy, ".", " ")
}
