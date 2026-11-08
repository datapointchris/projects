package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/datapointchris/projects/model"
)

// Once reads the board the way it opens and returns the frame it draws at width
// by height, for a caller with no terminal. A failed read is returned rather than
// drawn, so it reaches the exit code.
func (m *App) Once(width, height int) (string, error) {
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	if err := m.Read(); err != nil {
		return "", err
	}
	return lastRows(m.screen(), height), nil
}

// Read runs the board's opening reads in this goroutine: the projects, then the
// items of the one under the cursor.
func (m *App) Read() error {
	if err := m.apply(fetchProjectsCmd(m.backend, m.projectStatusFilter())); err != nil {
		return err
	}
	if cmd := m.fetchItems(); cmd != nil {
		return m.apply(cmd)
	}
	return nil
}

func (m *App) apply(cmd tea.Cmd) error {
	msg := cmd()
	if failed, ok := msg.(errMsg); ok {
		return failed.error
	}
	m.Update(msg)
	return nil
}

// Shown is what the board holds after Read: its projects, the one under the
// cursor, and that project's items as the pane lists them.
func (m *App) Shown() ([]model.ProjectWithItemCount, *model.ProjectWithItemCount, []model.ProjectItemInProject) {
	var selected *model.ProjectWithItemCount
	if m.projectCursor < len(m.projects) {
		selected = &m.projects[m.projectCursor]
	}
	return m.projects, selected, m.items
}
