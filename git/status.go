package git

import (
	"context"
	"fmt"
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
	out, err := c.gitCmd(ctx, "status", "--porcelain=v2").Output()
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}

	return parsePorcelainV2(string(out)), nil
}

// BranchStatus is what the `#` header lines of `git status --porcelain=v2
// --branch` say about HEAD.
type BranchStatus struct {
	// Head is the branch name, "" on a detached HEAD.
	Head string
	// OID is the full hash of HEAD, "" before the first commit.
	OID string
	// Upstream is the upstream's short name ("origin/main"), "" without one.
	Upstream string
	// HasAB is false when the upstream is gone: Ahead and Behind then mean
	// nothing.
	HasAB         bool
	Ahead, Behind int
}

// Status returns the branch facts and the working-tree entries from one
// `git status --porcelain=v2 --branch`.
func (c *Client) Status(ctx context.Context) (BranchStatus, []StatusEntry, error) {
	out, err := c.gitCmd(ctx, "status", "--porcelain=v2", "--branch").Output()
	if err != nil {
		return BranchStatus{}, nil, fmt.Errorf("git status: %w", err)
	}

	return parseBranchHeaders(string(out)), parsePorcelainV2(string(out)), nil
}

// parseBranchHeaders reads the `# branch.*` lines of porcelain=v2 output;
// anything else is skipped.
func parseBranchHeaders(raw string) BranchStatus {
	var bs BranchStatus
	for line := range strings.SplitSeq(raw, "\n") {
		key, val, ok := strings.Cut(strings.TrimPrefix(line, "# branch."), " ")
		if !ok || !strings.HasPrefix(line, "# branch.") {
			continue
		}
		switch key {
		case "oid":
			if val != "(initial)" {
				bs.OID = val
			}
		case "head":
			if val != "(detached)" {
				bs.Head = val
			}
		case "upstream":
			bs.Upstream = val
		case "ab":
			if n, _ := fmt.Sscanf(val, "+%d -%d", &bs.Ahead, &bs.Behind); n == 2 {
				bs.HasAB = true
			}
		}
	}

	return bs
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
