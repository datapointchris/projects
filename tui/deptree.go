package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/datapointchris/todoui/backend"
	"github.com/datapointchris/todoui/graph"
	"github.com/datapointchris/todoui/model"
)

type depTreePane int

const (
	forestPane depTreePane = iota
	focusPane
)

// depTreeState is the dependency browser: every tree on the left, the one you
// are standing in on the right.
//
// The two panes answer different questions. The forest is the map and stays
// put, so searching it is how you find work you could not have named. The focus
// pane is the working surface — it re-roots as you drill and flips direction
// independently, and losing your place on the map to do that is what made the
// earlier single-view version useless for browsing.
type depTreeState struct {
	tree   *graph.Tree
	lookup map[string]graph.ItemNode

	showFinished bool
	query        string
	searching    bool

	forestRows   []graph.Row
	forestCursor int
	forestScroll int

	focusRoots  []string
	focusRows   []graph.Row
	focusCursor int
	focusScroll int
	focusInvert bool
	stack       []depTreeFrame
	// pinned is the item the forest cursor selected, marked in the focus pane
	// so drilling several levels down does not lose where you came in.
	pinned string

	active depTreePane
}

type depTreeFrame struct {
	roots  []string
	invert bool
	cursor int
}

type depTreeMsg struct {
	tree   *graph.Tree
	lookup map[string]graph.ItemNode
	// focusID is the item to open on, or empty to open at the top of the forest.
	focusID string
}

// fetchDepTreeCmd reads the whole store in four queries. Every item is fetched
// whatever is being drawn: an edge crosses status and project boundaries, so a
// filtered read severs edges and splits one tree into several.
func fetchDepTreeCmd(b backend.Backend, focusID string) tea.Cmd {
	return func() tea.Msg {
		items, err := b.ListAllItemsIncludingArchived()
		if err != nil {
			return errMsg{err}
		}
		deps, err := b.ListDependencies()
		if err != nil {
			return errMsg{err}
		}
		memberships, err := b.ListMemberships()
		if err != nil {
			return errMsg{err}
		}
		projects, err := b.ListProjectsByStatus(model.StatusAll)
		if err != nil {
			return errMsg{err}
		}
		names := make(map[string]string, len(projects))
		for _, p := range projects {
			names[p.ID] = p.Name
		}
		tree, lookup := graph.BuildItems(items, deps, memberships, names)
		return depTreeMsg{tree: tree, lookup: lookup, focusID: focusID}
	}
}

// --- Forest ---

// rebuildForest redraws the left pane. Trees with no open work are left out
// unless asked for, since finished chains accumulate and would bury the live
// ones.
func (s *depTreeState) rebuildForest() {
	if s.tree == nil {
		s.forestRows = nil
		return
	}
	var roots []string
	for _, component := range s.tree.Components() {
		if !s.showFinished && !graph.HasOpenWork(component, s.lookup) {
			continue
		}
		roots = append(roots, s.tree.Roots(component, false)...)
	}
	rows := s.tree.Rows(roots, false, graph.Unlimited)
	if s.query != "" {
		rows = graph.FilterRows(rows, s.rowMatches)
	}
	s.forestRows = rows
	if s.forestCursor >= len(rows) {
		s.forestCursor = len(rows) - 1
	}
	if s.forestCursor < 0 {
		s.forestCursor = 0
	}
}

// rowMatches is the search, run over what a row displays: its number, its
// title, and the repo it is tagged with.
func (s *depTreeState) rowMatches(id string) bool {
	node := s.lookup[id]
	query := strings.ToLower(s.query)
	if strings.Contains(strings.ToLower(node.Item.Title), query) {
		return true
	}
	if strings.Contains(strings.ToLower(itemHandle(node.Item)), query) {
		return true
	}
	if node.Item.Repo != nil && strings.Contains(strings.ToLower(*node.Item.Repo), query) {
		return true
	}
	return false
}

// standOn puts the forest cursor on an item, revealing the finished trees if
// that is the only place it appears. It reports false when the item is in no
// tree at all, which the caller says out loud rather than opening somewhere
// else in silence.
func (s *depTreeState) standOn(id string) bool {
	if s.findInForest(id) {
		return true
	}
	if !s.tree.Connected(id) {
		return false
	}
	s.showFinished = true
	s.rebuildForest()
	return s.findInForest(id)
}

func (s *depTreeState) findInForest(id string) bool {
	for i, row := range s.forestRows {
		if row.ID == id {
			s.forestCursor = i
			return true
		}
	}
	return false
}

func (s *depTreeState) forestID() string {
	if s.forestCursor < 0 || s.forestCursor >= len(s.forestRows) {
		return ""
	}
	return s.forestRows[s.forestCursor].ID
}

// --- Focus ---

// focusOn points the right pane at the tree holding id, drawn from that tree's
// own roots rather than from the item — "the tree I am in" rather than "what
// this one item waits on", which drilling reaches anyway.
func (s *depTreeState) focusOn(id string) {
	s.pinned = id
	s.stack = nil
	s.focusInvert = false
	if id == "" || s.tree == nil {
		s.focusRoots = nil
		s.focusRows = nil
		return
	}
	members := s.tree.ComponentOf(id)
	if members == nil {
		s.focusRoots = []string{id}
	} else {
		s.focusRoots = s.tree.Roots(members, false)
	}
	s.focusCursor = 0
	s.focusScroll = 0
	s.rebuildFocus()
	s.cursorToPinned()
}

// cursorToPinned puts the focus cursor on the item the forest selected, so
// entering the pane lands where you were rather than at the top of a tree you
// did not choose.
func (s *depTreeState) cursorToPinned() {
	for i, row := range s.focusRows {
		if row.ID == s.pinned {
			s.focusCursor = i
			return
		}
	}
}

func (s *depTreeState) rebuildFocus() {
	if s.tree == nil {
		s.focusRows = nil
		return
	}
	s.focusRows = s.tree.Rows(s.focusRoots, s.focusInvert, graph.Unlimited)
	if s.focusCursor >= len(s.focusRows) {
		s.focusCursor = len(s.focusRows) - 1
	}
	if s.focusCursor < 0 {
		s.focusCursor = 0
	}
}

func (s *depTreeState) focusID() string {
	if s.focusCursor < 0 || s.focusCursor >= len(s.focusRows) {
		return ""
	}
	return s.focusRows[s.focusCursor].ID
}

func (s *depTreeState) push(roots []string) {
	s.stack = append(s.stack, depTreeFrame{roots: s.focusRoots, invert: s.focusInvert, cursor: s.focusCursor})
	s.focusRoots = roots
	s.focusCursor = 0
	s.focusScroll = 0
	s.rebuildFocus()
}

// pop retraces one drill. It reports false at the bottom, where the pane is
// already showing the whole tree and the only way out is back to the forest.
func (s *depTreeState) pop() bool {
	if len(s.stack) == 0 {
		return false
	}
	frame := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	s.focusRoots = frame.roots
	s.focusInvert = frame.invert
	s.focusCursor = frame.cursor
	s.rebuildFocus()
	return true
}

// --- Keys ---

func (m *App) handleDepTreeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.depTree
	if s.searching {
		return m.handleDepTreeSearchKey(msg)
	}
	if s.tree == nil {
		if msg.String() == "esc" || msg.String() == "q" {
			m.closeDepTree()
		}
		return m, nil
	}

	switch msg.String() {
	case "esc", "q":
		m.closeDepTree()
		return m, nil

	case "tab":
		s.active = focusPane
		if s.active == focusPane && len(s.focusRows) == 0 {
			s.active = forestPane
		}
		return m, nil

	case "j", "down":
		m.depTreeMove(1)
		return m, nil

	case "k", "up":
		m.depTreeMove(-1)
		return m, nil

	case "g":
		m.depTreeJump(0)
		return m, nil

	case "G":
		m.depTreeJump(-1)
		return m, nil

	case "ctrl+d":
		m.depTreeMove(m.depTreePaneHeight() / 2)
		return m, nil

	case "ctrl+u":
		m.depTreeMove(-m.depTreePaneHeight() / 2)
		return m, nil

	case "l", "right":
		if s.active == forestPane {
			if len(s.focusRows) > 0 {
				s.active = focusPane
			}
			return m, nil
		}
		// In the focus pane, l drills: the row under the cursor becomes the
		// root. A row with nothing under it in this direction would redraw the
		// same single line, so it says why rather than looking broken.
		id := s.focusID()
		if id == "" {
			return m, nil
		}
		if len(s.tree.Children(id, s.focusInvert)) == 0 {
			flashCmd := m.flash(m.depTreeDeadEnd())
			return m, flashCmd
		}
		s.push([]string{id})
		return m, nil

	case "h", "left":
		if s.active == focusPane && !s.pop() {
			s.active = forestPane
		} else if s.active == forestPane {
			m.closeDepTree()
		}
		return m, nil

	case "i":
		if s.active == forestPane {
			flashCmd := m.flash("Flip works in the focused tree")
			return m, flashCmd
		}
		s.stack = append(s.stack, depTreeFrame{roots: s.focusRoots, invert: s.focusInvert, cursor: s.focusCursor})
		s.focusInvert = !s.focusInvert
		// Re-root from the other end, or an inverted draw from the same roots
		// shows only the roots themselves.
		if members := s.tree.ComponentOf(s.focusID()); members != nil {
			s.focusRoots = s.tree.Roots(members, s.focusInvert)
		}
		s.focusCursor = 0
		s.focusScroll = 0
		s.rebuildFocus()
		s.cursorToPinned()
		return m, nil

	case "a":
		s.showFinished = !s.showFinished
		s.rebuildForest()
		s.forestScroll = syncScroll(s.forestCursor, s.forestScroll, m.depTreePaneHeight())
		return m, nil

	case "/":
		s.searching = true
		m.titleInput.SetValue(s.query)
		m.titleInput.Placeholder = "Filter trees..."
		return m, m.titleInput.Focus()

	case "enter":
		id := s.activeID()
		if id == "" {
			return m, nil
		}
		m.detailFromTree = true
		return m, fetchItemDetailCmd(m.backend, id, m.blockedSet[id])

	case "u":
		return m, undoCmd(m.backend)
	}

	return m, nil
}

func (m *App) handleDepTreeSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.depTree
	switch msg.String() {
	case "enter", "esc":
		if msg.String() == "esc" {
			s.query = ""
		} else {
			s.query = m.titleInput.Value()
		}
		s.searching = false
		m.titleInput.Blur()
		s.forestCursor = 0
		s.forestScroll = 0
		s.rebuildForest()
		s.focusOn(s.forestID())
		return m, nil
	default:
		cmd := m.updateDepTreeSearch(msg)
		return m, cmd
	}
}

// updateDepTreeSearch hands typing or a paste to the prompt and filters as it
// changes, so the tree narrows under the keystrokes rather than only when the
// prompt is dismissed.
func (m *App) updateDepTreeSearch(msg tea.Msg) tea.Cmd {
	s := &m.depTree
	var cmd tea.Cmd
	m.titleInput, cmd = m.titleInput.Update(msg)
	s.query = m.titleInput.Value()
	s.forestCursor = 0
	s.forestScroll = 0
	s.rebuildForest()
	s.focusOn(s.forestID())
	return cmd
}

// activeID is the item under whichever pane has focus.
func (s *depTreeState) activeID() string {
	if s.active == focusPane {
		return s.focusID()
	}
	return s.forestID()
}

func (m *App) depTreeMove(delta int) {
	s := &m.depTree
	height := m.depTreePaneHeight()
	if s.active == focusPane {
		s.focusCursor = clampCursor(s.focusCursor+delta, len(s.focusRows))
		s.focusScroll = syncScroll(s.focusCursor, s.focusScroll, height)
		return
	}
	before := s.forestCursor
	s.forestCursor = clampCursor(s.forestCursor+delta, len(s.forestRows))
	s.forestScroll = syncScroll(s.forestCursor, s.forestScroll, height)
	// Moving on the map repoints the working pane, which is the whole reason
	// the two are side by side.
	if s.forestCursor != before {
		s.focusOn(s.forestID())
	}
}

func (m *App) depTreeJump(to int) {
	s := &m.depTree
	if s.active == focusPane {
		if to < 0 {
			to = len(s.focusRows) - 1
		}
		s.focusCursor = clampCursor(to, len(s.focusRows))
		s.focusScroll = syncScroll(s.focusCursor, s.focusScroll, m.depTreePaneHeight())
		return
	}
	if to < 0 {
		to = len(s.forestRows) - 1
	}
	s.forestCursor = clampCursor(to, len(s.forestRows))
	s.forestScroll = syncScroll(s.forestCursor, s.forestScroll, m.depTreePaneHeight())
	s.focusOn(s.forestID())
}

func clampCursor(value, length int) int {
	if value >= length {
		value = length - 1
	}
	if value < 0 {
		value = 0
	}
	return value
}

// depTreeDeadEnd names what is missing in the direction being read, since "no
// children" means two different things depending on which way the edges run.
func (m *App) depTreeDeadEnd() string {
	if m.depTree.focusInvert {
		return "Nothing depends on this item"
	}
	return "This item depends on nothing"
}

// depTreeMiss explains why the browser did not open where it was asked to.
func (m *App) depTreeMiss(id string) string {
	return fmt.Sprintf("%s has no dependencies — showing every tree", itemHandle(m.depTree.lookup[id].Item))
}

func (m *App) closeDepTree() {
	m.appMode = modeNormal
	m.depTree = depTreeState{}
	m.detailFromTree = false
	m.titleInput.Blur()
}

// openDepTree enters the browser. An empty id opens at the top of the forest,
// which is how it is reached without having to find an item first.
func (m *App) openDepTree(itemID string) tea.Cmd {
	m.depTree = depTreeState{}
	return fetchDepTreeCmd(m.backend, itemID)
}

func (m *App) depTreePaneHeight() int {
	statusHeight := 1
	height := m.height - statusHeight - 4 // borders and the pane title
	if height < 3 {
		height = 3
	}
	return height
}

// --- Rendering ---

func (m *App) renderDepTreeView() string {
	statusBar := m.renderDepTreeStatusBar()
	statusHeight := lipgloss.Height(statusBar)

	paneHeight := m.height - statusHeight - 2
	if paneHeight < 3 {
		paneHeight = 3
	}

	leftWidth := m.width / 2
	if leftWidth < 24 {
		leftWidth = 24
	}
	rightWidth := m.width - leftWidth - 2
	if rightWidth < 20 {
		rightWidth = 20
	}

	left := m.renderForestPane(leftWidth-2, paneHeight)
	right := m.renderFocusPane(rightWidth-2, paneHeight)

	var leftBorder, rightBorder lipgloss.Style
	if m.depTree.active == forestPane {
		leftBorder = activeBorderStyle.Width(leftWidth).Height(paneHeight + 2)
		rightBorder = inactiveBorderStyle.Width(rightWidth).Height(paneHeight + 2)
	} else {
		leftBorder = inactiveBorderStyle.Width(leftWidth).Height(paneHeight + 2)
		rightBorder = activeBorderStyle.Width(rightWidth).Height(paneHeight + 2)
	}

	panes := lipgloss.JoinHorizontal(lipgloss.Top, leftBorder.Render(left), rightBorder.Render(right))
	return lipgloss.JoinVertical(lipgloss.Left, panes, statusBar)
}

func (m *App) renderForestPane(width, height int) string {
	s := &m.depTree

	title := "All trees"
	if s.showFinished {
		title = "All trees (incl. finished)"
	}
	lines := []string{paneTitleStyle.Render(title)}
	viewHeight := height - 1

	if s.searching {
		lines = append(lines, inputView(m.titleInput))
		viewHeight--
	} else if s.query != "" {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("/%s", s.query)))
		viewHeight--
	}

	switch {
	case s.tree == nil:
		lines = append(lines, emptyStyle.Render("Loading..."))
		return strings.Join(lines, "\n")
	case len(s.forestRows) == 0 && s.query != "":
		lines = append(lines, emptyStyle.Render("No item matches"))
		return strings.Join(lines, "\n")
	case len(s.forestRows) == 0:
		lines = append(lines, emptyStyle.Render("No dependency trees"))
		return strings.Join(lines, "\n")
	}

	showIndicator := len(s.forestRows) > viewHeight
	if showIndicator {
		viewHeight--
	}
	if viewHeight < 1 {
		viewHeight = 1
	}
	s.forestScroll = syncScroll(s.forestCursor, s.forestScroll, viewHeight)

	end := s.forestScroll + viewHeight
	if end > len(s.forestRows) {
		end = len(s.forestRows)
	}
	for i := s.forestScroll; i < end; i++ {
		lines = append(lines, m.depTreeRow(s.forestRows[i], i == s.forestCursor, false, width))
	}
	if showIndicator {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  %d/%d", s.forestCursor+1, len(s.forestRows))))
	}
	return strings.Join(lines, "\n")
}

func (m *App) renderFocusPane(width, height int) string {
	s := &m.depTree

	direction := "waits on"
	if s.focusInvert {
		direction = "unblocks"
	}
	lines := []string{paneTitleStyle.Render("Tree — " + direction)}
	viewHeight := height - 1

	switch {
	case s.tree == nil:
		lines = append(lines, emptyStyle.Render("Loading..."))
		return strings.Join(lines, "\n")
	case len(s.focusRows) == 0:
		lines = append(lines, emptyStyle.Render("Nothing selected"))
		return strings.Join(lines, "\n")
	case len(s.focusRows) == 1 && len(s.stack) == 0:
		lines = append(lines, m.depTreeRow(s.focusRows[0], s.active == focusPane, true, width))
		lines = append(lines, "")
		lines = append(lines, emptyStyle.Render("No dependencies either way"))
		return strings.Join(lines, "\n")
	}

	showIndicator := len(s.focusRows) > viewHeight
	if showIndicator {
		viewHeight--
	}
	if viewHeight < 1 {
		viewHeight = 1
	}
	s.focusScroll = syncScroll(s.focusCursor, s.focusScroll, viewHeight)

	end := s.focusScroll + viewHeight
	if end > len(s.focusRows) {
		end = len(s.focusRows)
	}
	for i := s.focusScroll; i < end; i++ {
		selected := i == s.focusCursor && s.active == focusPane
		lines = append(lines, m.depTreeRow(s.focusRows[i], selected, true, width))
	}
	if showIndicator {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  %d/%d", s.focusCursor+1, len(s.focusRows))))
	}
	return strings.Join(lines, "\n")
}

// depTreeRow draws one row. markPinned puts a dot on the item the forest
// selected, so a drill several levels deep still shows where you came in.
func (m *App) depTreeRow(row graph.Row, selected, markPinned bool, width int) string {
	s := &m.depTree
	node := s.lookup[row.ID]

	mark := "○"
	switch {
	case node.Item.Archived:
		mark = "▪"
	case node.Item.Completed:
		mark = "✓"
	}

	pin := " "
	if markPinned && row.ID == s.pinned {
		pin = "•"
	}

	prefix := fmt.Sprintf("%s%s%s %s ", pin, graph.Prefix(row), mark, itemHandle(node.Item))
	suffix := ""
	if tag := graph.CrossProjectTag(node, s.lookup[row.Parent], row.Parent != ""); tag != "" {
		suffix += " [" + tag + "]"
	}
	if row.Repeated {
		suffix += " (*)"
	}

	avail := width - lipgloss.Width(prefix) - lipgloss.Width(suffix) - 2
	if avail < 4 {
		avail = 4
	}
	line := prefix + truncate(node.Item.Title, avail) + suffix

	if selected {
		return itemSelectedStyle.Render(line)
	}
	if node.Item.Completed || node.Item.Archived {
		return itemCompletedStyle.Render(line)
	}
	return itemNormalStyle.Render(line)
}

func (m *App) renderDepTreeStatusBar() string {
	s := &m.depTree
	right := dimStyle.Render(fmt.Sprintf("%d trees", m.depTreeCount()))
	rightWidth := lipgloss.Width(right)
	// Clipped rather than wrapped, for the reason renderStatusBar gives: a
	// second row here pushes the panes off the top of the window. Only the
	// plain hint lines are truncated — cutting a styled message by rune would
	// slice an escape sequence in half.
	avail := m.width - rightWidth - 5

	var left string
	switch {
	case m.errorMsg != "":
		left = errorMsgStyle.Render("Error: " + m.errorMsg)
	case m.statusMsg != "":
		left = statusMsgStyle.Render(m.statusMsg)
	case s.searching:
		left = truncate("[Enter]apply [Esc]clear", avail)
	case s.active == forestPane:
		left = truncate("[j/k]move [l]focus [/]search [a]finished [Enter]read [Esc]back", avail)
	default:
		left = truncate("[j/k]move [l]drill [h]back [i]flip [Enter]read [Esc]back", avail)
	}

	padding := m.width - lipgloss.Width(left) - rightWidth - 4
	if padding < 1 {
		padding = 1
	}
	return statusBarStyle.Render(fmt.Sprintf(" %s%s%s ", left, strings.Repeat(" ", padding), right))
}

func (m *App) depTreeCount() int {
	s := &m.depTree
	if s.tree == nil {
		return 0
	}
	count := 0
	for _, component := range s.tree.Components() {
		if s.showFinished || graph.HasOpenWork(component, s.lookup) {
			count++
		}
	}
	return count
}
