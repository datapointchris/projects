package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/datapointchris/gotermboard"

	"github.com/datapointchris/projects/model"
)

// rowKind is what a cursor stop in the item pane is: a project's header in a grouped view,
// an item, or one of an item's tasks.
type rowKind int

const (
	rowGroup rowKind = iota
	rowItem
	rowTask
)

// paneRow is one cursor stop in the item pane. group indexes m.groups in a grouped view and is
// -1 otherwise. itemIdx indexes m.items and taskIdx the item's tasks, each -1 where the row is
// not one. groupRow and itemRow are the rows holding this one, which the sticky band draws.
type paneRow struct {
	kind     rowKind
	group    int
	itemIdx  int
	taskIdx  int
	groupRow int
	itemRow  int
}

// filing is one project an item is filed in.
type filing struct {
	projectID string
	name      string
}

// titleColumn is where an item's title, the line under it, its note, its tasks and its
// blockers start, so they read as one block.
const titleColumn = 7

// The band holds at most bandMost lines, and at most a bandShare of the list.
const (
	bandMost  = 5
	bandShare = 3
)

// matches says query keeps a row whose words are texts. A query with no capital in it ignores
// case, as ripgrep's --smart-case does.
func matches(query string, texts ...string) bool {
	if query == "" {
		return true
	}
	exact := strings.ContainsFunc(query, unicode.IsUpper)
	if !exact {
		query = strings.ToLower(query)
	}
	for _, text := range texts {
		if !exact {
			text = strings.ToLower(text)
		}
		if strings.Contains(text, query) {
			return true
		}
	}
	return false
}

func noteOf(item model.ProjectItem) string {
	if item.Notes == nil {
		return ""
	}
	return *item.Notes
}

func hasNote(item model.ProjectItem) bool { return strings.TrimSpace(noteOf(item)) != "" }

// itemShown says the item filter keeps the item, matching its title and its note.
func (m *App) itemShown(i int) bool {
	item := m.items[i].ProjectItem
	return matches(m.itemQuery, item.Title, noteOf(item))
}

// groupEnd is where the items of group gi stop in m.items.
func (m *App) groupEnd(gi int) int {
	if gi+1 < len(m.groups) {
		return m.groups[gi+1].startIndex
	}
	return len(m.items)
}

// groupCounts is the open and done items group gi shows under the filter, which its header
// carries whether it is folded or not.
func (m *App) groupCounts(gi int) (open, done int) {
	for i := m.groups[gi].startIndex; i < m.groupEnd(gi); i++ {
		if !m.itemShown(i) {
			continue
		}
		if m.items[i].Completed {
			done++
		} else {
			open++
		}
	}
	return open, done
}

// cursorKey names the row under the cursor by what it shows, so a rebuild that folds, filters
// or reads again keeps the cursor on the same thing. It reads m.items, so it is taken before
// a read replaces them.
func (m *App) cursorKey() string {
	row := m.currentRow()
	if row == nil {
		return ""
	}
	return m.keyOf(*row)
}

// keyOf names a row in a group under its group's project too, because an item filed in two
// projects shows under both.
func (m *App) keyOf(r paneRow) string {
	under := ""
	if r.group >= 0 {
		under = m.groups[r.group].projectID + "/"
	}
	switch r.kind {
	case rowGroup:
		return "group:" + m.groups[r.group].projectID
	case rowTask:
		return "task:" + under + m.itemTasks[m.items[r.itemIdx].ID][r.taskIdx].ID
	}
	return "item:" + under + m.items[r.itemIdx].ID
}

// rebuildRows lays the item pane's cursor stops out from m.items, the groups, the filter and
// the folds, then puts the cursor back on keep where it still shows.
func (m *App) rebuildRows(keep string) {
	var rows []paneRow
	addItem := func(group, groupRow, i int) {
		itemRow := len(rows)
		rows = append(rows, paneRow{kind: rowItem, group: group, itemIdx: i, taskIdx: -1, groupRow: groupRow, itemRow: -1})
		for ti := range m.itemTasks[m.items[i].ID] {
			rows = append(rows, paneRow{kind: rowTask, group: group, itemIdx: i, taskIdx: ti, groupRow: groupRow, itemRow: itemRow})
		}
	}
	if m.isGroupedView() {
		for gi, g := range m.groups {
			var shown []int
			for i := g.startIndex; i < m.groupEnd(gi); i++ {
				if m.itemShown(i) {
					shown = append(shown, i)
				}
			}
			if len(shown) == 0 && m.itemQuery != "" {
				continue
			}
			groupRow := len(rows)
			rows = append(rows, paneRow{kind: rowGroup, group: gi, itemIdx: -1, taskIdx: -1, groupRow: -1, itemRow: -1})
			if m.folded[g.projectID] {
				continue
			}
			for _, i := range shown {
				addItem(gi, groupRow, i)
			}
		}
	} else {
		for i := range m.items {
			if m.itemShown(i) {
				addItem(-1, -1, i)
			}
		}
	}
	m.rows = rows
	if keep != "" {
		if at := slices.IndexFunc(rows, func(r paneRow) bool { return m.keyOf(r) == keep }); at >= 0 {
			m.rowCursor = at
		}
	}
	m.rowCursor = max(0, min(m.rowCursor, len(m.rows)-1))
}

// currentRow returns the row under the cursor, or nil if the item pane is empty.
func (m *App) currentRow() *paneRow {
	if m.rowCursor < 0 || m.rowCursor >= len(m.rows) {
		return nil
	}
	return &m.rows[m.rowCursor]
}

// currentItem is the item the cursor's row belongs to: the item, or a task's item. It is nil
// on a group or an empty pane.
func (m *App) currentItem() *model.ProjectItemInProject {
	row := m.currentRow()
	if row == nil || row.kind == rowGroup {
		return nil
	}
	return &m.items[row.itemIdx]
}

func (m *App) onKind(kind rowKind) bool {
	row := m.currentRow()
	return row != nil && row.kind == kind
}

// firstRowForItem returns the row of the item at itemIdx, or 0 where it does not show.
func (m *App) firstRowForItem(itemIdx int) int {
	for i, r := range m.rows {
		if r.kind == rowItem && r.itemIdx == itemIdx {
			return i
		}
	}
	return 0
}

// depthOf is how deep a row sits: a group outermost, then an item, then its tasks.
func depthOf(r paneRow) int {
	switch r.kind {
	case rowGroup:
		return 0
	case rowItem:
		return 1
	}
	return 2
}

// jumpRow is [ and ]: the next or previous group or item at the cursor's depth or shallower,
// skipping tasks. Going back from a task lands on its own item first, as Vim's [[ does.
func (m *App) jumpRow(direction int) {
	row := m.currentRow()
	if row == nil {
		return
	}
	level := min(depthOf(*row), 1)
	for at := m.rowCursor + direction; at >= 0 && at < len(m.rows); at += direction {
		if r := m.rows[at]; r.kind != rowTask && depthOf(r) <= level {
			m.moveCursorTo(at)
			return
		}
	}
}

func (m *App) moveCursorTo(at int) {
	m.rowCursor = max(0, min(at, len(m.rows)-1))
	m.scrolled = false
}

// onOpenText says the cursor's row shows its note open under it, which ctrl+d, ctrl+u, J and K
// then read through.
func (m *App) onOpenText() bool {
	row := m.currentRow()
	return row != nil && row.kind == rowItem && m.noteOpen(m.items[row.itemIdx].ProjectItem)
}

func (m *App) noteOpen(item model.ProjectItem) bool {
	return m.opened[item.ID] && hasNote(item)
}

// --- Layout ---

// paneLayout is where every row of the item pane lands in line space. Scrolling counts lines
// rather than rows, because a row is worth several: an item's two lines, its open note, a
// group's blank, a blocker.
type paneLayout struct {
	// blockStart is the row's first line, counting the blank above a group. anchor is the
	// first line that has to stay on screen, which skips that blank.
	blockStart []int
	anchor     []int
	height     []int
	total      int
}

// rowAt returns the row whose block contains the given line.
func (l paneLayout) rowAt(line int) int {
	lo, hi := 0, len(l.blockStart)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if l.blockStart[mid] <= line {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// itemPaneLayout measures every row without drawing it, because the viewport needs every
// height to place itself while only the rows it lands on need their strings built.
func (m *App) itemPaneLayout(width int) paneLayout {
	l := paneLayout{
		blockStart: make([]int, len(m.rows)),
		anchor:     make([]int, len(m.rows)),
		height:     make([]int, len(m.rows)),
	}
	for i, row := range m.rows {
		lead := 0
		if row.kind == rowGroup && i > 0 {
			lead = 1
		}
		body := 1
		if row.kind == rowItem {
			body = 2
			if item := m.items[row.itemIdx].ProjectItem; m.noteOpen(item) {
				body += len(m.noteLines(item, width))
			}
		}
		if row.kind != rowGroup && m.rowIsLastOfItem(i) {
			body += len(m.itemBlockers[m.items[row.itemIdx].ID])
		}
		l.blockStart[i] = l.total
		l.anchor[i] = l.total + lead
		l.height[i] = lead + body
		l.total += l.height[i]
	}
	return l
}

// rowIsLastOfItem reports whether an item's blockers hang off this row.
func (m *App) rowIsLastOfItem(i int) bool {
	return i == len(m.rows)-1 || m.rows[i+1].kind != rowTask || m.rows[i+1].itemIdx != m.rows[i].itemIdx
}

// ownersAt is the rows whose first line holds the line at line, outermost first: an item's
// group, then the item for a line under its first. The sticky band draws them.
func (m *App) ownersAt(l paneLayout, line int) []int {
	if line < 0 || line >= l.total {
		return nil
	}
	r := l.rowAt(line)
	row := m.rows[r]
	var owners []int
	switch row.kind {
	case rowGroup:
		return nil
	case rowItem:
		if row.groupRow >= 0 {
			owners = append(owners, row.groupRow)
		}
		if line > l.anchor[r] {
			owners = append(owners, r)
		}
	case rowTask:
		if row.groupRow >= 0 {
			owners = append(owners, row.groupRow)
		}
		owners = append(owners, row.itemRow)
	}
	return owners
}

// stickyBand is the rows covering the top of the list, as VS Code's sticky scroll covers an
// editor's: the owners of the first line the band leaves showing, the innermost where they do
// not all fit. Each owner the band adds covers one more line, so this looks again until the
// lines it covers need no more.
func (m *App) stickyBand(l paneLayout, top, height int) []int {
	room := min(bandMost, height/bandShare)
	covered := 0
	for {
		band := m.ownersAt(l, top+covered)
		band = band[max(len(band)-room, 0):]
		if len(band) <= covered {
			return band
		}
		covered = len(band)
	}
}

// placeItemScroll keeps the cursor's row in view, unless the open text is being read through,
// and keeps it out from under the band, which is no cursor stop.
func (m *App) placeItemScroll(l paneLayout, height int) {
	c := m.rowCursor
	if c < 0 || c >= len(m.rows) {
		m.rowScroll = 0
		return
	}
	if !m.scrolled {
		end := l.blockStart[c] + l.height[c]
		m.rowScroll = scrollToBlock(l.anchor[c], end-l.anchor[c], m.rowScroll, height)
	}
	m.rowScroll = max(0, min(m.rowScroll, l.total-height))
	if !m.scrolled {
		for m.rowScroll > 0 && l.anchor[c] < m.rowScroll+len(m.stickyBand(l, m.rowScroll, height)) {
			m.rowScroll--
		}
	}
}

// scrollToBlock places the viewport so that the block a row renders into is visible. A block
// taller than the viewport is pinned to its first line, since its tail would hide the row the
// cursor is on.
func scrollToBlock(start, span, scroll, viewHeight int) int {
	if viewHeight <= 0 {
		return 0
	}
	span = min(span, viewHeight)
	if start < scroll {
		return start
	}
	if start+span > scroll+viewHeight {
		return start + span - viewHeight
	}
	return scroll
}

// --- Drawing ---

func (m *App) renderItemPane(width, height int) string {
	lines := []string{paneTitleStyle.Render(m.itemPaneTitle(width))}
	if line, ok := m.filterLine(itemPane); ok {
		lines = append(lines, line)
	}
	if len(m.rows) == 0 {
		switch {
		case m.itemQuery != "":
			lines = append(lines, emptyStyle.Render("No item matches"))
		case m.filter != filterNone:
			lines = append(lines, emptyStyle.Render("No matching items"))
		default:
			lines = append(lines, emptyStyle.Render("No items"))
		}
		return strings.Join(lines, "\n")
	}

	layout := m.itemPaneLayout(width)
	viewHeight := height - len(lines)
	showIndicator := layout.total > viewHeight
	if showIndicator {
		viewHeight--
	}
	viewHeight = max(viewHeight, 1)
	m.placeItemScroll(layout, viewHeight)

	window := m.renderItemWindow(width, layout, m.rowScroll, viewHeight)
	for k, r := range m.stickyBand(layout, m.rowScroll, viewHeight) {
		if k < len(window) {
			window[k] = m.rowFirstLine(r, width)
		}
	}
	lines = append(lines, window...)
	if showIndicator {
		lines = append(lines, dimStyle.Render(fmt.Sprintf(" %d/%d", m.rowCursor+1, len(m.rows))))
	}
	return strings.Join(lines, "\n")
}

func (m *App) itemPaneTitle(width int) string {
	var title string
	switch {
	case m.view == viewItems:
		title = "All items"
	case len(m.selectedProjects) > 0:
		title = fmt.Sprintf("Selected (%d)", len(m.selectedProjects))
	case m.projectCursor < len(m.projects):
		title = m.projects[m.projectCursor].Name
	default:
		title = "Items"
	}
	var label string
	switch m.filter {
	case filterBlocked:
		label = " [BLOCKED]"
	case filterAll:
		label = " [ALL]"
	}
	// A project name is free text and routinely longer than the pane, and one that wraps at
	// the border costs the list a line the scroll never counted.
	title = truncate(title, max(width-1-lipgloss.Width(label), 1))
	if label != "" {
		title += filterIndicatorStyle.Render(label)
	}
	return title
}

// renderItemWindow draws only the rows the viewport lands on.
func (m *App) renderItemWindow(width int, layout paneLayout, from, count int) []string {
	if count <= 0 || from >= layout.total || len(m.rows) == 0 {
		return nil
	}
	i := layout.rowAt(from)
	skip := from - layout.blockStart[i]
	var built []string
	for ; i < len(m.rows) && len(built) < skip+count; i++ {
		built = append(built, m.renderRowBlock(i, width)...)
	}
	if skip >= len(built) {
		return nil
	}
	built = built[skip:]
	return built[:min(len(built), count)]
}

// renderRowBlock draws one row and what hangs under it. Its line count must match what
// itemPaneLayout measured, which TestLayoutHeightMatchesWhatGetsRendered holds it to.
func (m *App) renderRowBlock(i, width int) []string {
	row := m.rows[i]
	var lines []string
	switch row.kind {
	case rowGroup:
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, m.groupLine(row.group, i == m.rowCursor, width))
		return lines
	case rowItem:
		item := m.items[row.itemIdx]
		lines = append(lines, m.itemLines(item, row.group, i == m.rowCursor, width)...)
		if m.noteOpen(item.ProjectItem) {
			lines = append(lines, m.noteLines(item.ProjectItem, width)...)
		}
	case rowTask:
		lines = append(lines, m.taskLine(row, i == m.rowCursor, width))
	}
	if m.rowIsLastOfItem(i) {
		lines = append(lines, m.blockerLines(m.items[row.itemIdx], row.group, width)...)
	}
	return lines
}

// rowFirstLine is a row's own line, as the sticky band draws it.
func (m *App) rowFirstLine(r, width int) string {
	row := m.rows[r]
	if row.kind == rowGroup {
		return m.groupLine(row.group, r == m.rowCursor, width)
	}
	return m.itemLines(m.items[row.itemIdx], row.group, r == m.rowCursor, width)[0]
}

// chevron is the cell beside a row's bullet that says space opens or folds it, and is blank
// where space would change nothing.
func chevron(opens, open bool) string {
	switch {
	case !opens:
		return " "
	case open:
		return "▾"
	}
	return "▸"
}

func (m *App) groupLine(gi int, cursor bool, width int) string {
	g := m.groups[gi]
	open, done := m.groupCounts(gi)
	lead := "  "
	if cursor {
		lead = "> "
	}
	lead += chevron(true, !m.folded[g.projectID]) + " "
	counts := projectCounts(open, done)
	name := truncate(g.projectName, max(width-lipgloss.Width(lead)-6-projectCountsWidth(open, done)-1, 1))
	style := groupHeaderStyle
	if cursor {
		style = cursorStyle
	}
	return ansi.Truncate(style.Render(lead+"── "+name+" ──")+" "+counts, width, "…")
}

// itemLines is an item's two lines: its state, title and glyphs, then dim, its repo, the other
// projects it is filed in and its number at the right edge.
func (m *App) itemLines(item model.ProjectItemInProject, group int, cursor bool, width int) []string {
	moving := m.appMode == modeMove && m.isMovingItem(item.ID)
	lead := "   "
	switch {
	case moving:
		lead = "▶ "
	case cursor:
		lead = "> "
	}
	status := "○"
	if item.Completed {
		status = "✓"
	}
	opens := hasNote(item.ProjectItem)
	head := lead + chevron(opens, m.opened[item.ID]) + " " + status + " "

	var glyphs []string
	var plain string
	if item.ProjectCount > 1 {
		plain += " ◈"
		glyphs = append(glyphs, multiProjectStyle.Render(" ◈"))
	}
	if opens {
		plain += " ≡"
		glyphs = append(glyphs, notesIndicatorStyle.Render(" ≡"))
	}
	if tc, ok := m.taskCounts[item.ID]; ok {
		text := fmt.Sprintf(" [%d/%d]", tc[0], tc[1])
		plain += text
		glyphs = append(glyphs, dimStyle.Render(text))
	}
	tail := ""
	if moving {
		tail = " ◀ MOVING"
	}
	// The title is what gives when the row will not fit, because a line wider than the pane
	// wraps at its border and spends a line the scroll never counted.
	title := truncate(item.Title, max(width-lipgloss.Width(head+plain+tail), 1))

	var first string
	switch {
	case moving:
		first = moveIndicatorStyle.Render(head + title + plain + tail)
	case item.Completed && cursor:
		first = cursorStyle.Render(head) + completedStyle.Render(title+plain)
	case item.Completed:
		first = completedStyle.Render(head + title + plain)
	case cursor:
		first = cursorStyle.Render(head+title) + strings.Join(glyphs, "")
	default:
		first = head + title + strings.Join(glyphs, "")
	}
	return []string{ansi.Truncate(first, width, "…"), m.identityLine(item.ProjectItem, group, width)}
}

// identityLine is an item's second line: the repo it is tagged with and the other projects it
// is filed in on the left, and its number right-aligned, all dim.
func (m *App) identityLine(item model.ProjectItem, group int, width int) string {
	number := itemHandle(item)
	var parts []string
	if item.Repo != nil && *item.Repo != "" {
		parts = append(parts, *item.Repo)
	}
	if others := m.otherProjects(item.ID, group); len(others) > 0 {
		parts = append(parts, "also in "+strings.Join(others, ", "))
	}
	room := max(width-titleColumn-lipgloss.Width(number)-2, 1)
	identity := truncate(strings.Join(parts, " · "), room)
	gap := max(width-titleColumn-lipgloss.Width(identity)-lipgloss.Width(number), 1)
	return dimStyle.Render(strings.Repeat(" ", titleColumn) + identity + strings.Repeat(" ", gap) + number)
}

// otherProjects is the projects an item is filed in besides the one the pane or its group
// already names.
func (m *App) otherProjects(itemID string, group int) []string {
	here := ""
	switch {
	case group >= 0:
		here = m.groups[group].projectID
	case m.projectCursor < len(m.projects):
		here = m.projects[m.projectCursor].ID
	}
	var others []string
	for _, f := range m.itemFilings[itemID] {
		if f.projectID != here {
			others = append(others, f.name)
		}
	}
	return others
}

func (m *App) taskLine(row paneRow, cursor bool, width int) string {
	t := m.itemTasks[m.items[row.itemIdx].ID][row.taskIdx]
	lead := strings.Repeat(" ", titleColumn-2)
	if cursor {
		lead += "> "
	} else {
		lead += "  "
	}
	check := "○"
	if t.Completed {
		check = "✓"
	}
	title := truncate(t.Title, max(width-titleColumn-2, 1))
	switch {
	case cursor && t.Completed:
		return cursorStyle.Render(lead+check+" ") + completedStyle.Render(title)
	case cursor:
		return cursorStyle.Render(lead + check + " " + title)
	case t.Completed:
		return lead + check + " " + completedStyle.Render(title)
	}
	return lead + check + " " + title
}

// noteLines is an item's note laid out by gotermboard's markdown renderer, under its title.
func (m *App) noteLines(item model.ProjectItem, width int) []string {
	room := max(min(width-titleColumn-1, gotermboard.ReadingWidth), 10)
	indent := strings.Repeat(" ", titleColumn)
	var lines []string
	for _, line := range m.markdown.Lines(noteOf(item), room) {
		lines = append(lines, ansi.Truncate(indent+line.Paint(m.palette), width, "…"))
	}
	return lines
}

// blockerLines is what hangs below an item: each item that blocks it.
func (m *App) blockerLines(item model.ProjectItemInProject, group int, width int) []string {
	here := ""
	switch {
	case group >= 0:
		here = m.groups[group].projectID
	case !m.isGroupedView() && m.projectCursor < len(m.projects):
		here = m.projects[m.projectCursor].ID
	}
	indent := strings.Repeat(" ", titleColumn)
	var lines []string
	for _, b := range m.itemBlockers[item.ID] {
		// A blocker filed elsewhere is named by its project, and one in the project already
		// on screen would only repeat the pane title.
		prefix := ""
		if filed := m.itemFilings[b.ID]; len(filed) > 0 && !slices.ContainsFunc(filed, func(f filing) bool { return f.projectID == here }) {
			prefix = truncate(filed[0].name, max(width/4, 4)) + ": "
		}
		handle := itemHandle(b)
		skeleton := func(p string) int {
			return lipgloss.Width(fmt.Sprintf("%s└─ blocked by: %s (%s)", indent, p, handle))
		}
		// A narrow pane gives up the owning project before the blocker's own title.
		if skeleton(prefix) >= width {
			prefix = ""
		}
		styledPrefix := ""
		if prefix != "" {
			styledPrefix = blockerProjectStyle.Render(prefix)
		}
		budget := max(width-skeleton(prefix), 1)
		lines = append(lines, ansi.Truncate(indent+blockerStyle.Render(
			fmt.Sprintf("└─ blocked by: %s%s (%s)", styledPrefix, truncate(b.Title, budget), handle),
		), width, "…"))
	}
	return lines
}
