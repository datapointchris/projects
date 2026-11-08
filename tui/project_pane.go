package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/datapointchris/projects/model"
)

// projectCounts renders a project's open and done tallies. The glyphs are the
// item pane's own — ○ for open, ✓ for done — so the two panes read as one
// vocabulary, and a zero goes dim rather than disappearing so the column stays
// aligned down the list.
func projectCounts(open, done int) string {
	openText := projectOpenCountStyle.Render(fmt.Sprintf("○%d", open))
	if open == 0 {
		openText = dimStyle.Render(fmt.Sprintf("○%d", open))
	}
	doneText := projectDoneCountStyle.Render(fmt.Sprintf("✓%d", done))
	if done == 0 {
		doneText = dimStyle.Render(fmt.Sprintf("✓%d", done))
	}
	return openText + " " + doneText
}

// projectCountsWidth is the printable width of what projectCounts renders,
// measured off the unstyled text because lipgloss.Width would have to strip the
// color codes back out again.
func projectCountsWidth(open, done int) int {
	return lipgloss.Width(fmt.Sprintf("○%d ✓%d", open, done))
}

// projectRowLabel is the text half of a project row. The width calculation and
// the render both go through it, so the pane is always wide enough for the label.
func (m *App) projectRowLabel(p model.ProjectWithItemCount) string {
	label := p.Name
	if m.selectedProjects[p.ID] {
		label = "● " + label
	}
	// Only while closed projects are on show: every row reads "active"
	// otherwise, which spends width to say nothing.
	if m.showClosed && p.Status != model.StatusActive {
		label = fmt.Sprintf("%s [%s]", label, p.Status)
	}
	return label
}

// projectPaneWidth fits the widest row of every project read, so the pane holds still while
// the filter narrows the list.
func (m *App) projectPaneWidth() int {
	const minWidth = 20
	widest := 0
	for _, p := range m.allProjects {
		// "> name ○open ✓done" — 2 prefix + name + space + counts
		widest = max(widest, lipgloss.Width("> "+m.projectRowLabel(p)+" ")+projectCountsWidth(p.OpenCount, p.CompletedCount))
	}
	// 4 is the border and a column of air either side.
	width := max(widest+4, minWidth)
	// cap at 40% of terminal to protect the item pane
	return min(width, m.width*2/5)
}

func (m *App) renderProjectPane(width, height int) string {
	lines := []string{paneTitleStyle.Render("Projects")}
	if line, ok := m.filterLine(projectPane); ok {
		lines = append(lines, line)
	}

	if len(m.projects) == 0 {
		empty := "No projects"
		if m.projectQuery != "" {
			empty = "No project matches"
		}
		lines = append(lines, emptyStyle.Render(empty))
		return strings.Join(lines, "\n")
	}

	viewHeight := height - len(lines)
	// The scroll indicator is a line the list does not get.
	showIndicator := len(m.projects) > viewHeight
	if showIndicator {
		viewHeight--
	}
	viewHeight = max(viewHeight, 1)
	m.projectScroll = syncScroll(m.projectCursor, m.projectScroll, viewHeight)
	end := min(m.projectScroll+viewHeight, len(m.projects))

	// Only the name is truncated. Slicing the finished line by byte would cut a
	// multi-byte rune in half as soon as a name is long enough to reach the ● or
	// the count glyphs.
	row := func(prefix, name string, open, done int, style lipgloss.Style) string {
		avail := max(width-lipgloss.Width(prefix)-style.GetPaddingLeft()-projectCountsWidth(open, done)-1, 1)
		return style.Render(prefix+truncate(name, avail)) + " " + projectCounts(open, done)
	}

	for pi := m.projectScroll; pi < end; pi++ {
		p := m.projects[pi]
		name := m.projectRowLabel(p)
		if m.appMode == modeMoveProject && pi == m.moveProjectPos {
			const lead, marker = "▶ ", " ◀ MOVING"
			name = truncate(name, max(width-lipgloss.Width(lead+marker), 1))
			lines = append(lines, moveIndicatorStyle.Render(lead+name+marker))
			continue
		}
		prefix := "  "
		style := projectNormalStyle
		switch {
		case pi == m.projectCursor:
			prefix = "> "
			style = projectSelectedStyle
		case m.selectedProjects[p.ID]:
			style = projectMultiSelectedStyle
		}
		lines = append(lines, row(prefix, name, p.OpenCount, p.CompletedCount, style))
	}

	if showIndicator {
		lines = append(lines, dimStyle.Render(fmt.Sprintf(" %d/%d", m.projectCursor+1, len(m.projects))))
	}
	return strings.Join(lines, "\n")
}
