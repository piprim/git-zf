package tui

import (
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// minBand is the fewest rows either band may have: under it the screen is
// not split.
const minBand = 10

// layout is how the terminal is shared: git-zf draws in the rows 1..top,
// the last of which is a separator, and the terminal's scroll region keeps
// the output of the commands in the rows under it.
type layout struct {
	rows, cols int
	top        int
}

// newLayout gives size percent of the rows to git-zf. It reports false when
// one of the two bands would be under minBand.
func newLayout(rows, cols, size int) (layout, bool) {
	l := layout{rows: rows, cols: cols, top: rows * size / 100} //nolint:mnd // a percentage

	return l, l.fits()
}

// fits reports whether both bands have their least height.
func (l layout) fits() bool { return l.top >= minBand && l.rows-l.top >= minBand }

// region confines the scrolling to the output band. The terminal moves the
// cursor home when it is set.
func (l layout) region() string { return fmt.Sprintf("\x1b[%d;%dr", l.top+1, l.rows) }

// open scrolls what the terminal shows into its scrollback (newlines from
// the last row, not an erase), sets the region and parks the output cursor
// on its first row.
func (l layout) open() string {
	return fmt.Sprintf("\x1b[%d;1H", l.rows) + strings.Repeat("\n", l.rows) + l.region() +
		fmt.Sprintf("\x1b[%d;1H", l.top+1)
}

// before saves the output cursor and moves to git-zf's band. The region is
// set again each time: a full-screen child (an editor) may have reset it.
func (l layout) before() string { return "\x1b7" + l.region() + "\x1b[1;1H" }

// after redraws the separator, which bubbletea erased with the last line of
// the view, sets the region for the command that follows (a terminal may
// have dropped it on a resize) and gives the cursor back to the output.
func (l layout) after() string {
	return fmt.Sprintf("\x1b[%d;1H", l.top) + strings.Repeat("─", l.cols) + l.region() + "\x1b8"
}

// resized moves the region's bottom to rows and puts the cursor back where
// bubbletea's renderer left it: the start of the band's last line.
func (l layout) resized(rows int) string {
	return fmt.Sprintf("\x1b[%d;%dr\x1b[%d;1H", l.top+1, rows, l.top)
}

// closeSeq gives the whole terminal back: the region is reset, git-zf's
// band deleted so the output slides to the top, and the cursor put back
// where the output left it (resetting the region moved it home).
func (l layout) closeSeq() string {
	return fmt.Sprintf("\x1b7\x1b[r\x1b[1;1H\x1b[%dM\x1b8\x1b[%dA", l.top, l.top)
}

// band confines a screen to git-zf's band: the screen is told the band's
// height and its view is cut or padded to exactly that many lines. With a
// constant line count bubbletea's inline renderer never erases below the
// view, so it never touches the output band.
type band struct {
	inner tea.Model
	l     layout
	out   io.Writer
}

func (b *band) Init() tea.Cmd { return b.inner.Init() }

func (b *band) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	forward := msg
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		// The first size a program is told is the one it started in: only
		// a real resize sets the region again (a terminal may drop it on
		// any of them, a change of width included).
		if ws.Height != b.l.rows || ws.Width != b.l.cols {
			b.l.rows, b.l.cols = ws.Height, ws.Width
			// A region whose bottom is not under its top is invalid.
			if ws.Height > b.l.top {
				_, _ = io.WriteString(b.out, b.l.resized(ws.Height))
			}
		}
		ws.Height = b.l.top - 1
		forward = ws
	}
	var cmd tea.Cmd
	b.inner, cmd = b.inner.Update(forward)

	return b, cmd
}

func (b *band) View() string {
	lines := strings.Split(strings.TrimSuffix(b.inner.View(), "\n"), "\n")
	for len(lines) < b.l.top-1 {
		lines = append(lines, "")
	}

	return strings.Join(lines[:b.l.top-1], "\n") + "\n" + strings.Repeat("─", b.l.cols)
}
