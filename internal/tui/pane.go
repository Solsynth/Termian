package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// contentWidth is the usable text width inside a pane, given the terminal.
func (m *Model) contentWidth() int {
	w := m.Width - 6
	if w < 20 {
		w = 20
	}
	if w > 110 {
		w = 110
	}
	return w
}

// bodyHeight is how many lines a pane body may use, leaving room for the
// title, the hint and the status line.
func (m *Model) bodyHeight() int {
	h := m.Height - 6
	if m.Banner != "" {
		h -= 2
	}
	if h < 3 {
		h = 3
	}
	return h
}

// pane renders a full-width screen: the section strip, a title row, the body
// and a hint row.
func (m *Model) pane(title, right string, body []string, hint string) string {
	width := m.contentWidth()
	body = clipLines(body, m.bodyHeight())
	head := m.Styles.Title.Render(title)
	if right != "" {
		pad := width - ansi.StringWidth(title) - ansi.StringWidth(right)
		if pad < 1 {
			pad = 1
		}
		head += strings.Repeat(" ", pad) + m.Styles.Muted.Render(right)
	}

	lines := make([]string, 0, len(body)+4)
	lines = append(lines, m.sectionHeader(), head, "")
	lines = append(lines, body...)
	if hint != "" {
		lines = append(lines, "", m.Styles.Hint.Render(truncate(hint, width)))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// clipLines keeps at most n lines.
func clipLines(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[:n]
}

// wrapText wraps text to width and splits it into lines.
func wrapText(text string, width int) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	if width < 1 {
		width = 1
	}
	wrapped := ansi.WordwrapWc(text, width, "")
	return strings.Split(wrapped, "\n")
}

// truncate shortens s to width cells, appending an ellipsis when cut.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}

// shortTime renders a timestamp compactly: time of day today, date before.
func shortTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.Local()
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	if t.Year() == now.Year() {
		return t.Format("Jan 2")
	}
	return t.Format("2006-01-02")
}

// ago renders how long ago t happened, in coarse units.
func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return shortTime(t)
	}
}

// scrollWindow returns the visible slice [start,end) for a selection.
func scrollWindow(sel, offset, count, height int) (start, end int) {
	if height < 1 {
		height = 1
	}
	if sel < offset {
		offset = sel
	}
	if sel >= offset+height {
		offset = sel - height + 1
	}
	if offset < 0 {
		offset = 0
	}
	start = offset
	end = min(start+height, count)
	return start, end
}

// plural picks the singular or plural word for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// visibleCards picks the card indices that fit into height lines while
// keeping sel on screen. It fills forwards from the selection and, when there
// is room left, adds earlier cards for context.
func visibleCards(lines [][]string, sel, height int) []int {
	if len(lines) == 0 {
		return nil
	}
	sel = max(0, min(sel, len(lines)-1))
	height = max(height, 1)

	start := sel
	used := len(lines[sel])
	// Pull earlier cards in while they still fit.
	for start > 0 && used+len(lines[start-1]) <= height {
		start--
		used += len(lines[start])
	}
	// Never start below the selection: the selection must stay visible.
	if start > sel {
		start = sel
	}
	end := sel + 1
	for end < len(lines) && used+len(lines[end]) <= height {
		used += len(lines[end])
		end++
	}
	out := make([]int, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, i)
	}
	return out
}

// splitPane renders a two-column screen: a title row, two bodies separated by a
// rule, and a hint row.
func (m *Model) splitPane(title, right string, left, rightBody []string, hint string) string {
	width := m.contentWidth()
	height := m.bodyHeight()

	// The list column keeps a fixed share so the detail column stays readable.
	listWidth := width * 2 / 5
	listWidth = max(24, min(listWidth, 44))
	detailWidth := width - listWidth - 3 // one space, the rule, one space
	if detailWidth < 20 {
		detailWidth = 20
	}

	left = clipLines(padLines(left, listWidth), height)
	rightBody = clipLines(padLines(rightBody, detailWidth), height)

	rule := m.Styles.Muted.Render("│")
	rows := make([]string, 0, height)
	for i := 0; i < height; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(rightBody) {
			r = rightBody[i]
		}
		rows = append(rows, padRight(l, listWidth)+" "+rule+" "+r)
	}

	head := m.Styles.Title.Render(title)
	if right != "" {
		gap := width - ansi.StringWidth(title) - ansi.StringWidth(right)
		if gap < 1 {
			gap = 1
		}
		head += strings.Repeat(" ", gap) + m.Styles.Muted.Render(right)
	}

	lines := make([]string, 0, height+4)
	lines = append(lines, m.sectionHeader(), head, "")
	lines = append(lines, rows...)
	if hint != "" {
		lines = append(lines, "", m.Styles.Hint.Render(truncate(hint, width)))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// padLines makes every line exactly width cells wide.
func padLines(lines []string, width int) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, padRight(line, width))
	}
	return out
}

// padRight pads a styled line to width cells.
func padRight(line string, width int) string {
	if gap := width - ansi.StringWidth(line); gap > 0 {
		return line + strings.Repeat(" ", gap)
	}
	return ansi.Truncate(line, width, "…")
}
