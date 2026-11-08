package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/datapointchris/gotermboard"

	"github.com/datapointchris/projects/model"
)

// dimCode is ANSI 8, the color dimStyle draws in, for the reader's quiet lines.
const dimCode = "\033[90m"

// openReader shows document full screen until esc or space closes it.
func (m *App) openReader(title string, document gotermboard.Document) {
	m.reader = gotermboard.NewReader(title, document, m.palette)
	m.reader.ClosesWith(func() { m.reader = nil })
	if m.width > 0 {
		m.reader.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	}
}

// documentLines builds a document a line at a time, wrapping each at the width it is laid out at.
type documentLines struct {
	width int
	lines []gotermboard.Line
}

func (d *documentLines) add(code, text string) {
	for _, row := range strings.Split(ansi.Wrap(text, d.width, ""), "\n") {
		d.lines = append(d.lines, gotermboard.Line{gotermboard.C(row, code)})
	}
}

// section opens a heading the reader's [ and ] land on. A note's own headings sit under it.
func (d *documentLines) section(title string) {
	d.lines = append(d.lines, nil, gotermboard.Line{{Text: title, Code: gotermboard.BoldBlue, Section: 1}})
}

// markdown lays out text as markdown, its headings one level under the section holding them.
func (d *documentLines) markdown(m *App, text string) {
	d.lines = append(d.lines, nil)
	for _, line := range m.markdown.Lines(text, d.width) {
		for i := range line {
			if line[i].Section > 0 {
				line[i].Section++
			}
		}
		d.lines = append(d.lines, line)
	}
}

// itemDocument is an item's page: where it is filed, what blocks it and what it blocks, its
// tasks, and its note.
func (m *App) itemDocument(msg itemDetailMsg) gotermboard.Document {
	return func(width int) []gotermboard.Line {
		d := &documentLines{width: width}
		item := msg.detail

		status := "○ open"
		switch {
		case item.Archived:
			status = "▪ archived"
		case item.Completed:
			status = "✓ done"
		}
		var projectNames []string
		inItemProjects := make(map[string]bool)
		for _, p := range item.Projects {
			projectNames = append(projectNames, p.Name)
			inItemProjects[p.Name] = true
		}
		d.add(dimCode, fmt.Sprintf("Item %s · %s", itemHandle(item.ProjectItem), status))
		d.add(dimCode, "Projects: "+strings.Join(projectNames, ", "))
		if item.Repo != nil && *item.Repo != "" {
			d.add(dimCode, "Repo: "+*item.Repo)
		}
		d.add(dimCode, "Created: "+item.CreatedAt.Format("Jan 2, 2006"))

		if len(msg.blockers) > 0 {
			d.section("Blocked by")
			for _, b := range msg.blockers {
				// A blocker filed outside this item's projects is named by its own.
				prefix := ""
				if names := msg.projectNames[b.ID]; len(names) > 0 && !anyIn(names, inItemProjects) {
					prefix = names[0] + ": "
				}
				d.add(gotermboard.Red, fmt.Sprintf("○ %s%s (%s)", prefix, b.Title, itemHandle(b)))
			}
		}

		if len(msg.dependents) > 0 {
			d.section("Blocks")
			for _, dep := range msg.dependents {
				switch {
				case dep.Archived:
					d.add(dimCode, fmt.Sprintf("▪ %s (%s)", dep.Title, itemHandle(dep)))
				case dep.Completed:
					d.add(dimCode, fmt.Sprintf("✓ %s (%s)", dep.Title, itemHandle(dep)))
				default:
					d.add("", fmt.Sprintf("○ %s (%s)", dep.Title, itemHandle(dep)))
				}
			}
		}

		if len(msg.tasks) > 0 {
			done := 0
			for _, t := range msg.tasks {
				if t.Completed {
					done++
				}
			}
			d.section(fmt.Sprintf("Tasks %d/%d", done, len(msg.tasks)))
			for _, t := range msg.tasks {
				if t.Completed {
					d.add(dimCode, "✓ "+t.Title)
				} else {
					d.add("", "○ "+t.Title)
				}
			}
		}

		if note := noteOf(item.ProjectItem); strings.TrimSpace(note) != "" {
			d.section("Notes")
			d.markdown(m, note)
		}
		return d.lines
	}
}

// projectDocument is a project's page: its status, its counts, and its description.
func (m *App) projectDocument(p model.ProjectWithItemCount) gotermboard.Document {
	return func(width int) []gotermboard.Line {
		d := &documentLines{width: width}
		d.add(dimCode, fmt.Sprintf("Project %s · %s", shortID(p.ID), p.Status))
		d.add(dimCode, fmt.Sprintf("○%d open · ✓%d done", p.OpenCount, p.CompletedCount))
		d.add(dimCode, "Created: "+p.CreatedAt.Format("Jan 2, 2006"))
		if p.Status != model.StatusActive {
			// A shelved project has not closed, so it has no closing line.
			if p.Status != model.StatusSomeday {
				closed := "Closed"
				if p.ClosedAt != nil {
					closed += ": " + p.ClosedAt.Format("Jan 2, 2006")
				}
				d.add(dimCode, closed)
			}
			if p.StatusReason != nil && *p.StatusReason != "" {
				d.add(dimCode, "Reason: "+*p.StatusReason)
			}
		}
		if p.Description != nil && strings.TrimSpace(*p.Description) != "" {
			d.section("Description")
			d.markdown(m, *p.Description)
		}
		return d.lines
	}
}

func anyIn(names []string, set map[string]bool) bool {
	for _, n := range names {
		if set[n] {
			return true
		}
	}
	return false
}
