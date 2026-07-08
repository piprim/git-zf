package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/piprim/git-zf/git"
)

type statusKind int

const (
	kindStaged statusKind = iota
	kindUnstaged
	kindUntracked
	kindUnmerged
)

// statusLine is one rendered entry: a git word ("modified", "new file", ...)
// and a path. Word is empty for untracked entries.
type statusLine struct {
	Word string
	Path string
}

// statusGroup is one colored section with its classified lines.
type statusGroup struct {
	Kind  statusKind
	Lines []statusLine
}

// unmergedWords maps porcelain conflict codes to git's long-format phrasing.
var unmergedWords = map[string]string{
	"DD": "both deleted",
	"AU": "added by us",
	"UD": "deleted by them",
	"UA": "added by them",
	"DU": "deleted by us",
	"AA": "both added",
	"UU": "both modified",
}

// wordForCode maps a single porcelain change code to its git long-format word.
func wordForCode(c byte) string {
	switch c {
	case 'A':
		return "new file"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "typechange"
	default: // 'M' and any other single-side change
		return "modified"
	}
}

// renamePath renders "orig -> path" for renames/copies, else the plain path.
func renamePath(e git.StatusEntry) string {
	if e.OrigPath != "" {
		return e.OrigPath + " -> " + e.Path
	}

	return e.Path
}

// mergeUnstagedIntoStaged appends unstaged lines to staged, skipping any path
// already present in staged (a file staged and further worktree-modified keeps
// its staged line). Used for the -a/--all case, where every tracked worktree
// change is committed.
func mergeUnstagedIntoStaged(staged, unstaged []statusLine) []statusLine {
	seen := make(map[string]struct{}, len(staged))
	for _, ln := range staged {
		seen[ln.Path] = struct{}{}
	}

	out := staged
	for _, ln := range unstaged {
		if _, ok := seen[ln.Path]; ok {
			continue
		}
		seen[ln.Path] = struct{}{}
		out = append(out, ln)
	}

	return out
}

// groupEntries classifies entries into ordered, non-empty sections. An entry
// with both index and worktree changes (e.g. "MM") contributes to both the
// staged and unstaged sections, matching `git status`.
//
// When all is true (the commit ran with -a/--all), every tracked worktree
// change is staged at commit time, so the unstaged tracked changes are folded
// into "Changes to be committed" (deduping a path already staged) and no
// separate unstaged section is emitted. Untracked files are never staged by
// -a, so they stay in their own section.
//
// When includeUntracked is true (the commit ran with -u/--include-untracked),
// untracked files are folded into "Changes to be committed" as "new file:"
// lines and the untracked section is omitted.
func groupEntries(entries []git.StatusEntry, all, includeUntracked bool) []statusGroup {
	var staged, unstaged, untracked, unmerged []statusLine

	for _, e := range entries {
		switch {
		case e.XY == "??":
			untracked = append(untracked, statusLine{Path: e.Path})
		case unmergedWords[e.XY] != "":
			unmerged = append(unmerged, statusLine{Word: unmergedWords[e.XY], Path: e.Path})
		case len(e.XY) == 2:
			if x := e.XY[0]; x != ' ' {
				staged = append(staged, statusLine{Word: wordForCode(x), Path: renamePath(e)})
			}
			if y := e.XY[1]; y != ' ' {
				unstaged = append(unstaged, statusLine{Word: wordForCode(y), Path: e.Path})
			}
		}
	}

	if all {
		staged = mergeUnstagedIntoStaged(staged, unstaged)
		unstaged = nil
	}

	// --include-untracked stages untracked files at commit time, so they show up
	// under "Changes to be committed" as `new file: <path>` (what git status
	// prints once an untracked file is staged), and no separate "Untracked
	// files:" section is emitted. Untracked paths never collide with staged
	// paths, so a plain append is correct. Independent of the `all` fold.
	if includeUntracked {
		for _, ln := range untracked {
			staged = append(staged, statusLine{Word: "new file", Path: ln.Path})
		}
		untracked = nil
	}

	var groups []statusGroup
	if len(staged) > 0 {
		groups = append(groups, statusGroup{Kind: kindStaged, Lines: staged})
	}
	if len(unstaged) > 0 {
		groups = append(groups, statusGroup{Kind: kindUnstaged, Lines: unstaged})
	}
	if len(untracked) > 0 {
		groups = append(groups, statusGroup{Kind: kindUntracked, Lines: untracked})
	}
	if len(unmerged) > 0 {
		groups = append(groups, statusGroup{Kind: kindUnmerged, Lines: unmerged})
	}

	return groups
}

// sectionMeta pairs a section's header label with its color.
type sectionMeta struct {
	header string
	color  lipgloss.Color
}

// sectionMetaByKind maps each section to its git long-format header and color:
// staged=yellow, unstaged=green, untracked=turquoise, unmerged=red.
var sectionMetaByKind = map[statusKind]sectionMeta{
	kindStaged:    {"Changes to be committed:", lipgloss.Color("3")},
	kindUnstaged:  {"Changes not staged for commit:", lipgloss.Color("2")},
	kindUntracked: {"Untracked files:", lipgloss.Color("#40E0D0")},
	kindUnmerged:  {"Unmerged paths:", lipgloss.Color("1")},
}

// formatLine renders one entry line: "word: path", or just "path" for untracked.
func formatLine(ln statusLine) string {
	if ln.Word == "" {
		return ln.Path
	}

	return ln.Word + ": " + ln.Path
}

// StatusPanel renders entries as a bordered lipgloss panel titled "Current Git
// Status", grouped into colored sections. Returns "" when there are no entries.
//
// When all is true (commit -a/--all), tracked worktree changes are shown under
// "Changes to be committed" instead of a separate "not staged" section, so the
// panel reflects what the commit will actually include. When includeUntracked
// is true (commit -u/--include-untracked), untracked files fold into "Changes
// to be committed" as "new file:" lines and the untracked section is omitted.
func StatusPanel(entries []git.StatusEntry, all, includeUntracked bool) string {
	groups := groupEntries(entries, all, includeUntracked)
	if len(groups) == 0 {
		return ""
	}

	var b strings.Builder
	for i, g := range groups {
		meta := sectionMetaByKind[g.Kind]
		style := lipgloss.NewStyle().Foreground(meta.color)

		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(style.Render(meta.header))
		b.WriteString("\n")

		for _, ln := range g.Lines {
			b.WriteString(style.Render("  " + formatLine(ln)))
			b.WriteString("\n")
		}
	}

	title := lipgloss.NewStyle().Bold(true).Render("Current Git Status")
	body := strings.TrimRight(b.String(), "\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)

	return box.Render(title + "\n\n" + body)
}

// StatusPanelReserveWidth returns the widest the panel can render for these
// entries across every combination of the --all and --include-untracked
// re-classifications. The form reserves this so its width stays stable when
// either toggle flips the panel between layouts. Returns 0 when nothing shows.
func StatusPanelReserveWidth(entries []git.StatusEntry) int {
	w := 0
	for _, all := range []bool{false, true} {
		for _, includeUntracked := range []bool{false, true} {
			if pw := lipgloss.Width(StatusPanel(entries, all, includeUntracked)); pw > w {
				w = pw
			}
		}
	}

	return w
}
