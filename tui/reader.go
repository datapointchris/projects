package tui

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	markdownansi "charm.land/glamour/v2/ansi"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	mdast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	mdtext "github.com/yuin/goldmark/text"

	"github.com/datapointchris/projects/model"
)

// The reader is a full screen showing one item or one project, its markdown
// rendered. A view keeps only the last `height` lines of a screen taller than the
// terminal, so a long note drawn whole would lose its top. The reader instead
// builds every row it could show, each already wrapped to the width, and draws
// the window of them that fits.

func ptr[T any](v T) *T { return &v }

// readerMarkdownStyle is the one style every rendered note and description
// uses. Its colors are ANSI 0–15, so they follow the terminal's theme, which
// glamour's built-in 256-color styles do not. Code blocks name no chroma theme,
// which leaves them unhighlighted: every chroma theme is 256-color too.
var readerMarkdownStyle = markdownansi.StyleConfig{
	BlockQuote: markdownansi.StyleBlock{
		StylePrimitive: markdownansi.StylePrimitive{Color: ptr("8"), Italic: ptr(true)},
		Indent:         ptr(uint(1)),
		IndentToken:    ptr("│ "),
	},
	List: markdownansi.StyleList{LevelIndent: 2},
	Heading: markdownansi.StyleBlock{
		StylePrimitive: markdownansi.StylePrimitive{BlockSuffix: "\n", Color: ptr("4"), Bold: ptr(true)},
	},
	H1: markdownansi.StyleBlock{StylePrimitive: markdownansi.StylePrimitive{Prefix: "# ", Color: ptr("6")}},
	H2: markdownansi.StyleBlock{StylePrimitive: markdownansi.StylePrimitive{Prefix: "## "}},
	H3: markdownansi.StyleBlock{StylePrimitive: markdownansi.StylePrimitive{Prefix: "### "}},
	H4: markdownansi.StyleBlock{StylePrimitive: markdownansi.StylePrimitive{Prefix: "#### "}},
	H5: markdownansi.StyleBlock{StylePrimitive: markdownansi.StylePrimitive{Prefix: "##### "}},
	H6: markdownansi.StyleBlock{StylePrimitive: markdownansi.StylePrimitive{Prefix: "###### ", Bold: ptr(false)}},

	Strikethrough:  markdownansi.StylePrimitive{CrossedOut: ptr(true)},
	Emph:           markdownansi.StylePrimitive{Italic: ptr(true)},
	Strong:         markdownansi.StylePrimitive{Bold: ptr(true)},
	HorizontalRule: markdownansi.StylePrimitive{Color: ptr("8"), Format: "\n────────\n"},

	Item:        markdownansi.StylePrimitive{BlockPrefix: "• "},
	Enumeration: markdownansi.StylePrimitive{BlockPrefix: ". "},
	Task:        markdownansi.StyleTask{Ticked: "[✓] ", Unticked: "[ ] "},

	Link:      markdownansi.StylePrimitive{Color: ptr("4"), Underline: ptr(true)},
	LinkText:  markdownansi.StylePrimitive{Color: ptr("6"), Bold: ptr(true)},
	Image:     markdownansi.StylePrimitive{Color: ptr("5"), Underline: ptr(true)},
	ImageText: markdownansi.StylePrimitive{Color: ptr("8"), Format: "Image: {{.text}} →"},

	Code: markdownansi.StyleBlock{StylePrimitive: markdownansi.StylePrimitive{Color: ptr("3")}},
	CodeBlock: markdownansi.StyleCodeBlock{
		StyleBlock: markdownansi.StyleBlock{
			StylePrimitive: markdownansi.StylePrimitive{Color: ptr("3")},
			Margin:         ptr(uint(2)),
		},
	},
	Table: markdownansi.StyleTable{
		CenterSeparator: ptr("┼"),
		ColumnSeparator: ptr("│"),
		RowSeparator:    ptr("─"),
	},
	DefinitionDescription: markdownansi.StylePrimitive{BlockPrefix: "\n→ "},
}

// markdownRender is the last markdown the reader rendered. The reader redraws
// on every key and rendering is not free, so the rows are kept until the
// entity, the width or the text changes.
type markdownRender struct {
	entityID string
	width    int
	source   string
	rows     []string
}

func (m *App) renderedMarkdown(entityID, source string, width int) []string {
	c := &m.readerMarkdown
	if c.rows != nil && c.entityID == entityID && c.width == width && c.source == source {
		return c.rows
	}
	*c = markdownRender{entityID: entityID, width: width, source: source, rows: renderMarkdown(source, width)}
	return c.rows
}

// renderMarkdown renders source to rows no wider than width. A text glamour
// cannot render is shown as written rather than not at all.
func renderMarkdown(source string, width int) []string {
	// A terminal draws a tab to the next eighth column, which the row wrap
	// counts as no width at all. Four spaces is what lipgloss draws.
	source = strings.ReplaceAll(source, "\t", "    ")
	out := source
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(readerMarkdownStyle),
		glamour.WithWordWrap(width),
	)
	if err == nil {
		if rendered, err := r.Render(htmlAsText(source)); err == nil {
			out = rendered
		}
	}
	return wrapRows(strings.Trim(out, "\n"), width)
}

// htmlParser parses as glamour does, so it finds the raw HTML glamour would.
var htmlParser = goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList)).Parser()

// htmlAsText escapes each `<` that opens raw HTML, which glamour would strip,
// so a placeholder like `<slug>` shows as written. It parses again until none
// is left, because escaping a block's opening tag can expose inline tags.
func htmlAsText(source string) string {
	src := []byte(source)
	for range bytes.Count(src, []byte("<")) {
		var at []int
		_ = mdast.Walk(htmlParser.Parse(mdtext.NewReader(src)), func(n mdast.Node, entering bool) (mdast.WalkStatus, error) {
			if !entering {
				return mdast.WalkContinue, nil
			}
			var segs []mdtext.Segment
			switch n := n.(type) {
			case *mdast.RawHTML:
				segs = n.Segments.Sliced(0, n.Segments.Len())
			case *mdast.HTMLBlock:
				segs = n.Lines().Sliced(0, n.Lines().Len())
				if n.HasClosure() {
					segs = append(segs, n.ClosureLine)
				}
			}
			for _, s := range segs {
				for i := s.Start; i < s.Stop; i++ {
					if src[i] == '<' {
						at = append(at, i)
					}
				}
			}
			return mdast.WalkContinue, nil
		})
		if len(at) == 0 {
			break
		}
		slices.Sort(at)
		var b bytes.Buffer
		prev := 0
		for _, i := range slices.Compact(at) {
			b.Write(src[prev:i])
			b.WriteByte('\\')
			prev = i
		}
		b.Write(src[prev:])
		src = b.Bytes()
	}
	return string(src)
}

// wrapRows splits s into the rows it occupies at width. glamour wraps prose
// but not a long token or a wide table row, and the scroll counts on one entry
// being one row on screen.
func wrapRows(s string, width int) []string {
	return strings.Split(ansi.Wrap(s, width, ""), "\n")
}

// styledRows wraps plain text, then styles each row on its own, so a row
// scrolled to the top of the screen still carries its style.
func styledRows(style lipgloss.Style, text string, width int) []string {
	rows := wrapRows(text, width)
	for i, r := range rows {
		rows[i] = style.Render(r)
	}
	return rows
}

func readerSection(title string) string {
	return dimStyle.Render("─── " + title + " ───")
}

func (m *App) readingItem() bool {
	return m.appMode == modeReader && m.itemDetail != nil
}

// refreshReaderCmd refetches what the reader shows, so a change made or undone
// from it reaches the screen. It is nil outside the reader.
func (m *App) refreshReaderCmd() tea.Cmd {
	switch {
	case m.readingItem():
		return fetchItemDetailCmd(m.backend, m.itemDetail.ID, m.blockedSet[m.itemDetail.ID])
	case m.appMode == modeReader && m.projectDetail != nil:
		return fetchProjectDetailCmd(m.backend, m.projectDetail.ID)
	}
	return nil
}

// readerWidth leaves a column of margin on each side.
func (m *App) readerWidth() int {
	return max(m.width-2, 10)
}

// readerHeight is the rows the text gets: all of them bar the status bar.
func (m *App) readerHeight() int {
	return max(m.height-1, 1)
}

func (m *App) readerRows(width int) []string {
	switch {
	case m.itemDetail != nil:
		return m.itemReaderRows(width)
	case m.projectDetail != nil:
		return m.projectReaderRows(width)
	}
	return nil
}

func (m *App) itemReaderRows(width int) []string {
	d := m.itemDetail
	var rows []string
	add := func(style lipgloss.Style, text string) {
		rows = append(rows, styledRows(style, text, width)...)
	}

	status := "○ open"
	if d.Completed {
		status = "✓ done"
	}
	if d.Archived {
		status = "▪ archived"
	}
	var projectNames []string
	inItemProjects := make(map[string]bool)
	for _, p := range d.Projects {
		projectNames = append(projectNames, p.Name)
		inItemProjects[p.Name] = true
	}

	add(readerTitleStyle, d.Title)
	add(dimStyle, fmt.Sprintf("Item %s · %s", itemHandle(d.ProjectItem), status))
	add(dimStyle, "Projects: "+strings.Join(projectNames, ", "))
	if d.Repo != nil && *d.Repo != "" {
		add(dimStyle, "Repo: "+*d.Repo)
	}
	add(dimStyle, "Created: "+d.CreatedAt.Format("Jan 2, 2006"))

	if len(m.detailBlockers) > 0 {
		rows = append(rows, "", readerSection("Blocked by"))
		for _, b := range m.detailBlockers {
			// A blocker filed outside this item's projects is named by its own.
			prefix := ""
			if names := m.detailBlockerProjects[b.ID]; len(names) > 0 && !anyIn(names, inItemProjects) {
				prefix = names[0] + ": "
			}
			add(readerBlockerStyle, fmt.Sprintf("○ %s%s (%s)", prefix, b.Title, itemHandle(b)))
		}
	}

	if len(m.detailDependents) > 0 {
		rows = append(rows, "", readerSection("Blocks"))
		for _, dep := range m.detailDependents {
			switch {
			case dep.Archived:
				add(dimStyle, fmt.Sprintf("▪ %s (%s)", dep.Title, itemHandle(dep)))
			case dep.Completed:
				add(taskCompletedStyle, fmt.Sprintf("✓ %s (%s)", dep.Title, itemHandle(dep)))
			default:
				add(lipgloss.NewStyle(), fmt.Sprintf("○ %s (%s)", dep.Title, itemHandle(dep)))
			}
		}
	}

	if len(m.detailTasks) > 0 {
		done := 0
		for _, t := range m.detailTasks {
			if t.Completed {
				done++
			}
		}
		rows = append(rows, "", readerSection(fmt.Sprintf("Tasks (%d/%d)", done, len(m.detailTasks))))
		for _, t := range m.detailTasks {
			if t.Completed {
				add(taskCompletedStyle, "✓ "+t.Title)
			} else {
				add(lipgloss.NewStyle(), "○ "+t.Title)
			}
		}
	}

	if d.Notes != nil && strings.TrimSpace(*d.Notes) != "" {
		rows = append(rows, "", readerSection("Notes"), "")
		rows = append(rows, m.renderedMarkdown(d.ID, *d.Notes, width)...)
	}
	return rows
}

func (m *App) projectReaderRows(width int) []string {
	p := m.projectDetail
	var rows []string
	add := func(style lipgloss.Style, text string) {
		rows = append(rows, styledRows(style, text, width)...)
	}

	add(readerTitleStyle, p.Name)
	add(dimStyle, fmt.Sprintf("Project %s · %s", shortID(p.ID), p.Status))
	rows = append(rows, projectCounts(p.OpenCount, p.CompletedCount)+dimStyle.Render("  open · done"))
	add(dimStyle, "Created: "+p.CreatedAt.Format("Jan 2, 2006"))
	if p.Status != model.StatusActive {
		// A shelved project has not closed, so it has no closing line.
		if p.Status != model.StatusSomeday {
			closed := "Closed"
			if p.ClosedAt != nil {
				closed += ": " + p.ClosedAt.Format("Jan 2, 2006")
			}
			add(dimStyle, closed)
		}
		if p.StatusReason != nil && *p.StatusReason != "" {
			add(dimStyle, "Reason: "+*p.StatusReason)
		}
	}

	if p.Description != nil && strings.TrimSpace(*p.Description) != "" {
		rows = append(rows, "", readerSection("Description"), "")
		rows = append(rows, m.renderedMarkdown(p.ID, *p.Description, width)...)
	}
	return rows
}

func anyIn(names []string, set map[string]bool) bool {
	for _, n := range names {
		if set[n] {
			return true
		}
	}
	return false
}

// clampScroll keeps the window inside the rows: never above the first, and
// never so far down that the last row leaves the bottom of the screen.
func clampScroll(scroll, total, height int) int {
	return max(min(scroll, total-height), 0)
}

func (m *App) scrollReader(delta int) {
	total := len(m.readerRows(m.readerWidth()))
	m.readerScroll = clampScroll(m.readerScroll+delta, total, m.readerHeight())
}

func (m *App) renderReader() string {
	rows := m.readerRows(m.readerWidth())
	height := m.readerHeight()
	m.readerScroll = clampScroll(m.readerScroll, len(rows), height)

	var b strings.Builder
	for i := m.readerScroll; i < m.readerScroll+height; i++ {
		if i < len(rows) {
			b.WriteString(" " + rows[i])
		}
		b.WriteByte('\n')
	}
	b.WriteString(m.renderReaderBar(len(rows), height))
	return b.String()
}

func (m *App) readerHints() string {
	const scroll = "[esc]back [j/k]scroll [^d/^u]half page [g/G]top/bottom"
	if m.itemDetail != nil {
		return scroll + " [space]done [e]dit [n]otes [p]rojects [t]ask [b]lock [B]unblock [T]ree [x]archive [u]ndo"
	}
	if m.projectDetail != nil && m.projectDetail.Status != model.StatusActive {
		return scroll + " [e]dit name [d]escription [r]eopen"
	}
	return scroll + " [e]dit name [d]escription [c]omplete [x]drop [S]helve"
}

// renderReaderBar is the reader's status bar: the hints, or a flash, on the
// left and how far down the text the window sits on the right.
func (m *App) renderReaderBar(total, height int) string {
	last := min(m.readerScroll+height, total)
	position := fmt.Sprintf("%d/%d", last, total)
	positionWidth := lipgloss.Width(position)

	var left string
	switch {
	case m.errorMsg != "":
		left = errorMsgStyle.Render("Error: " + m.errorMsg)
	case m.statusMsg != "":
		left = statusMsgStyle.Render(m.statusMsg)
	default:
		left = clipHints(m.readerHints(), m.width-positionWidth-5)
	}
	padding := max(m.width-lipgloss.Width(left)-positionWidth-4, 1)
	return statusBarStyle.Render(fmt.Sprintf(" %s%s%s ", left, strings.Repeat(" ", padding), position))
}

func (m *App) handleReaderKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	half := max(m.readerHeight()/2, 1)
	key := msg.String()
	switch key {
	case "esc", "q", "h", "left":
		m.leaveReader()
		return m, nil
	case "j", "down":
		m.scrollReader(1)
		return m, nil
	case "k", "up":
		m.scrollReader(-1)
		return m, nil
	case "ctrl+d", "pgdown":
		m.scrollReader(half)
		return m, nil
	case "ctrl+u", "pgup":
		m.scrollReader(-half)
		return m, nil
	case "g", "home":
		m.readerScroll = 0
		return m, nil
	case "G", "end":
		m.scrollReader(len(m.readerRows(m.readerWidth())))
		return m, nil
	case "u":
		return m, undoCmd(m.backend)
	}

	var cmd tea.Cmd
	switch {
	case m.itemDetail != nil:
		cmd = m.handleItemReaderKey(key)
	case m.projectDetail != nil:
		cmd = m.handleProjectReaderKey(key)
	}
	return m, cmd
}

// handleItemReaderKey sends an action key to the handler the list uses, aimed
// at the item on screen. Archiving, linking and the browser each end somewhere
// other than this item's text, so the reader closes onto the list first.
func (m *App) handleItemReaderKey(key string) tea.Cmd {
	d := m.itemDetail
	incomplete := false
	for _, t := range m.detailTasks {
		if !t.Completed {
			incomplete = true
		}
	}
	target := itemActionTarget{item: d.ProjectItem, hasIncompleteTasks: incomplete, blocked: m.blockedSet[d.ID]}

	switch key {
	case "x", "b", "B", "T":
		m.leaveReaderForList()
	}
	cmd, _ := m.handleItemActionKey(key, target, modeReader)
	return cmd
}

func (m *App) handleProjectReaderKey(key string) tea.Cmd {
	p := m.projectDetail
	switch key {
	case "e":
		m.titleInput.SetValue(p.Name)
		m.titleInput.Placeholder = ""
		m.returnMode = modeReader
		m.appMode = modeEditProjectName
		return m.titleInput.Focus()

	case "d":
		desc := ""
		if p.Description != nil {
			desc = *p.Description
		}
		return m.openInEditor(editTarget{projectID: p.ID}, projectDescriptionFileName(p.Project), desc)

	case "c":
		if p.Status != model.StatusActive {
			return m.flash("Not active — r reopens it")
		}
		return setProjectStatusCmd(m.backend, p.ID, model.StatusCompleted, nil)

	case "S":
		if p.Status != model.StatusActive {
			return m.flash("Not active — r reopens it")
		}
		// Shelving closes no question about the project, so unlike a drop it
		// asks for no reason.
		return setProjectStatusCmd(m.backend, p.ID, model.StatusSomeday, nil)

	case "x":
		if p.Status != model.StatusActive {
			return m.flash("Not active — r reopens it")
		}
		// A reason is required, so dropping is a prompt rather than a keypress:
		// "deferred" invites the same idea back, "dropped, and here is why" does not.
		m.titleInput.SetValue("")
		m.titleInput.Placeholder = "Why dropped rather than deferred?"
		m.returnMode = modeReader
		m.appMode = modeDropProject
		return m.titleInput.Focus()

	case "r":
		if p.Status == model.StatusActive {
			return m.flash("Already active")
		}
		return setProjectStatusCmd(m.backend, p.ID, model.StatusActive, nil)
	}
	return nil
}

// clearReader forgets what the reader shows. The scroll and the rendered
// markdown are kept: the first is reset by whoever opens the next entity, and
// the second checks its own key.
func (m *App) clearReader() {
	m.itemDetail = nil
	m.detailBlockers = nil
	m.detailBlockerProjects = nil
	m.detailDependents = nil
	m.detailTasks = nil
	m.projectDetail = nil
}

// leaveReader returns to where the reader was opened from: the dependency
// browser, or the list on the row the cursor was on.
func (m *App) leaveReader() {
	if m.detailFromTree && m.depTree.tree != nil {
		m.clearReader()
		m.detailFromTree = false
		m.appMode = modeDepTree
		return
	}
	m.leaveReaderForList()
}

func (m *App) leaveReaderForList() {
	m.clearReader()
	m.detailFromTree = false
	// The tree holds a snapshot of every item; drop it with the reader rather
	// than carrying it for the rest of the session.
	m.depTree = depTreeState{}
	m.appMode = modeNormal
}
