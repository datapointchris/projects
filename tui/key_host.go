package tui

import (
	tea "charm.land/bubbletea/v2"
)

// The board is the gotermboard.Host its universal rows act on. A key moves the cursor of the pane
// it works in: the dependency map's, the project list's, or the item list's.

// MoveBy is gotermboard.Host's.
func (m *App) MoveBy(rows int) tea.Cmd {
	if m.view == viewDependencies {
		m.depTreeMove(rows)
		return nil
	}
	return m.moveBy(rows)
}

// HalfScreen is gotermboard.Host's.
func (m *App) HalfScreen() int {
	if m.view == viewDependencies {
		return max(m.depTreePaneHeight()/2, 1)
	}
	return max(m.paneContentHeight()/2, 1)
}

// ScrollBy is gotermboard.Host's: it moves the item pane through the open detail by lines, and
// leaves the cursor on its row.
func (m *App) ScrollBy(lines int) {
	m.scrolled = true
	m.rowScroll = max(m.rowScroll+lines, 0)
}

// OnOpenText is gotermboard.Host's.
func (m *App) OnOpenText() bool {
	row := m.currentRow()
	return m.view != viewDependencies && row != nil && row.kind == rowItem && m.detailOpen(m.items[row.itemIdx].ID)
}

// JumpGroup is gotermboard.Host's.
func (m *App) JumpGroup(direction int) { m.jumpRow(direction) }

// HasGroups is gotermboard.Host's. An item is the group its tasks sit in.
func (m *App) HasGroups() bool { return m.view != viewDependencies && m.onItemList() }

// Toggles is gotermboard.Host's: enter and space open an item's detail or fold a project's
// group. The project list and the map bind their own.
func (m *App) Toggles() bool {
	if m.view == viewDependencies || m.view == viewProjects && m.activePane == projectPane {
		return false
	}
	return m.onKind(rowItem) || m.onKind(rowGroup)
}

// Open is gotermboard.Host's: enter reads an item's detail full screen where it runs past the
// pane, and otherwise opens or closes it in place. The detail is read first, so the choice is
// made on what it holds now.
func (m *App) Open() tea.Cmd {
	if m.onKind(rowGroup) {
		return m.toggleGroup()
	}
	item := m.currentItem()
	return fetchItemDetailCmd(m.backend, item.ID, m.blockedSet[item.ID], openByEnter)
}

// ToggleInPlace is gotermboard.Host's: space opens an item's detail in place however tall, or
// closes it.
func (m *App) ToggleInPlace() tea.Cmd {
	if m.onKind(rowGroup) {
		return m.toggleGroup()
	}
	item := m.currentItem()
	if m.detailOpen(item.ID) {
		m.closeDetail(item.ID)
		return nil
	}
	return fetchItemDetailCmd(m.backend, item.ID, m.blockedSet[item.ID], openInPlace)
}

// ShowKeys is gotermboard.Host's.
func (m *App) ShowKeys() {
	m.returnMode = m.appMode
	m.appMode = modeHelp
}

// Quit is gotermboard.Host's.
func (m *App) Quit() tea.Cmd { return tea.Quit }

// detailOpening is what an item's detail is read for.
type detailOpening int

const (
	// openFullScreen reads it on the full-screen reader.
	openFullScreen detailOpening = iota
	// openByEnter reads it full screen where it runs past the pane, and otherwise opens or
	// closes it in place.
	openByEnter
	// openInPlace opens it under the item's row however tall.
	openInPlace
	// openAgain lays out again a detail open in place, from what the board read since.
	openAgain
)

// detailOpen says the item's detail shows in place under its row.
func (m *App) detailOpen(itemID string) bool {
	_, read := m.details[itemID]
	return m.opened[itemID] && read
}

func (m *App) closeDetail(itemID string) {
	m.opened[itemID] = false
	delete(m.details, itemID)
	m.scrolled = false
}

// tallDetail says the detail runs past the item pane, so enter reads it full screen.
func (m *App) tallDetail(msg itemDetailMsg) bool {
	return len(m.itemDocument(msg)(detailWidth(m.itemPaneWidth()-2))) >= m.paneContentHeight()
}

// tookDetail is an item's detail read for opening.
func (m *App) tookDetail(msg itemDetailMsg) {
	id := msg.detail.ID
	switch msg.opening {
	case openFullScreen:
		m.openReader(msg.detail.Title, m.itemDocument(msg))
		return
	case openByEnter:
		if m.tallDetail(msg) {
			m.openReader(msg.detail.Title, m.itemDocument(msg))
			return
		}
		if m.detailOpen(id) {
			m.closeDetail(id)
			return
		}
	case openAgain:
		if !m.opened[id] {
			return
		}
	}
	m.details[id] = msg
	m.opened[id] = true
	m.scrolled = false
}

// readOpenDetailsAgain reads again each detail open in place, after the board read its items,
// so the text under a row says what the item holds now.
func (m *App) readOpenDetailsAgain() tea.Cmd {
	var cmds []tea.Cmd
	read := map[string]bool{}
	for _, item := range m.items {
		if m.detailOpen(item.ID) && !read[item.ID] {
			read[item.ID] = true
			cmds = append(cmds, fetchItemDetailCmd(m.backend, item.ID, m.blockedSet[item.ID], openAgain))
		}
	}
	return tea.Batch(cmds...)
}
