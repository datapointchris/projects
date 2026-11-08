package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/datapointchris/projects/graph"
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

// Read runs the board's opening reads in this goroutine: the projects, then what the view
// lists, which is the items of the project under the cursor, every item, or the dependency map.
func (m *App) Read() error {
	if err := m.apply(fetchProjectsCmd(m.backend, m.projectStatusFilter())); err != nil {
		return err
	}
	if m.view == viewDependencies {
		return m.apply(fetchDepTreeCmd(m.backend, ""))
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

// ShownItem is a row the item pane lists, with the project whose list or group it is under.
// An item filed in two projects shows once under each.
type ShownItem struct {
	Item        model.ProjectItemInProject
	ProjectID   string
	ProjectName string
}

// Shown is what the board holds after Read: its projects, the one under the cursor, and the
// items the item pane lists. All items has no project under a cursor, so it answers none.
func (m *App) Shown() ([]model.ProjectWithItemCount, *model.ProjectWithItemCount, []ShownItem) {
	var selected *model.ProjectWithItemCount
	if m.view == viewProjects && m.projectCursor < len(m.projects) {
		selected = &m.projects[m.projectCursor]
	}
	items := make([]ShownItem, len(m.items))
	for i, item := range m.items {
		items[i].Item = item
		if selected != nil {
			items[i].ProjectID, items[i].ProjectName = selected.ID, selected.Name
		}
	}
	if m.isGroupedView() {
		for gi, g := range m.groups {
			for i := g.startIndex; i < m.groupEnd(gi); i++ {
				items[i].ProjectID, items[i].ProjectName = g.projectID, g.projectName
			}
		}
	}
	return m.projects, selected, items
}

// ShownTrees is the dependency map after Read: the tree, what each of its items is, and the
// components the forest draws.
func (m *App) ShownTrees() (*graph.Tree, map[string]graph.ItemNode, [][]string) {
	return m.depTree.tree, m.depTree.lookup, m.depTree.shownComponents()
}
