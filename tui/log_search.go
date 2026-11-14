package tui

import (
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// escRe matches the escape sequences git colors a commit with.
var escRe = regexp.MustCompile(`\x1b\[[0-9;:?]*[A-Za-z]`)

// A match is shown in reverse video, written raw: the colors around it stay.
// The current one is yellow as well; ending it resets the foreground, so the
// colors the line had set before it are written again.
const (
	matchOn, matchOff     = "\x1b[7m", "\x1b[27m"
	currentOn, currentOff = "\x1b[7;33m", "\x1b[27;39m"
)

// dialogMatch is one occurrence of the searched text: its line, the cells
// before it and the cells it takes.
type dialogMatch struct{ line, col, width int }

// highlight returns text with every occurrence of q in reverse video, the
// current-th one (-1: none) in its own color, and where they are. The search
// ignores case and the colors of text.
func highlight(text, q string, current int) (string, []dialogMatch) {
	re := regexp.MustCompile("(?i)" + regexp.QuoteMeta(q))
	lines := strings.Split(text, "\n")
	var found []dialogMatch
	for i, l := range lines {
		plain := escRe.ReplaceAllString(l, "")
		spans := re.FindAllStringIndex(plain, -1)
		if spans == nil {
			continue
		}
		for _, s := range spans {
			found = append(found, dialogMatch{i, ansi.StringWidth(plain[:s[0]]), ansi.StringWidth(plain[s[0]:s[1]])})
		}
		lines[i] = reverseSpans(l, spans, current-len(found)+len(spans))
	}

	return strings.Join(lines, "\n"), found
}

// reverseSpans puts the spans of line in reverse video, spans[current] as
// the current match; they are byte offsets in the line without its escape
// sequences.
func reverseSpans(line string, spans [][]int, current int) string {
	var b, seen strings.Builder // seen: the escape sequences so far
	on := func() {
		if current == 0 {
			b.WriteString(currentOn)
		} else {
			b.WriteString(matchOn)
		}
	}
	off := func() {
		if current == 0 {
			b.WriteString(currentOff + seen.String())
		} else {
			b.WriteString(matchOff)
		}
		current--
	}
	esc := escRe.FindAllStringIndex(line, -1)
	p, in := 0, false // p: the offset in the line without its colors
	for i := 0; i < len(line); {
		if in && p == spans[0][1] {
			off()
			in, spans = false, spans[1:]
		}
		if len(esc) > 0 && esc[0][0] == i {
			b.WriteString(line[i:esc[0][1]])
			seen.WriteString(line[i:esc[0][1]])
			if in { // a reset inside a match would end it
				on()
			}
			i, esc = esc[0][1], esc[1:]

			continue
		}
		if len(spans) > 0 && p == spans[0][0] {
			on()
			in = true
		}
		b.WriteByte(line[i])
		i++
		p++
	}
	if in {
		off()
	}

	return b.String()
}

// search highlights q in the dialog and jumps, as less does, to the first
// match at or below the top line; an empty q clears the search.
func (m *logModel) search(q string) {
	m.query, m.matches, m.match = q, nil, 0
	if q != "" {
		_, m.matches = highlight(m.dialogText, q, -1)
	}
	if len(m.matches) == 0 {
		m.dialog.SetContent(m.dialogText)
		m.gotoMatch(0)

		return
	}
	first := 0
	for i, found := range m.matches {
		if found.line >= m.dialog.YOffset {
			first = i

			break
		}
	}
	m.gotoMatch(first)
}

// gotoMatch puts match i, wrapped around the ends, on the top line and
// scrolls sideways when it is off to the right.
func (m *logModel) gotoMatch(i int) {
	n := len(m.matches)
	if n == 0 {
		if m.query != "" {
			m.dialogStatus = "not found: " + m.query
		}

		return
	}
	m.match = (i + n) % n
	found := m.matches[m.match]
	text, _ := highlight(m.dialogText, m.query, m.match)
	m.dialog.SetContent(text)
	m.dialog.SetYOffset(found.line)
	x := 0
	if found.col+found.width > m.dialog.Width {
		x = found.col
	}
	m.dialog.SetXOffset(x)
}

// updateSearching handles the search prompt of the dialog.
func (m *logModel) updateSearching(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc", "enter":
			m.searching = false
			m.searchInput.Blur()
			if key.String() == "enter" {
				m.search(m.searchInput.Value())
			}

			return m, nil
		}
	}

	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)

	return m, cmd
}
