package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/datapointchris/gotermboard"
	"github.com/datapointchris/gotermboard/markdown"
	"github.com/datapointchris/gotermstyle"

	"github.com/datapointchris/projects/backend"
	"github.com/datapointchris/projects/model"
)

// App is the top-level Bubble Tea model for the TUI.
type App struct {
	backend backend.Backend
	view    view

	// allProjects is the project list as read, and projects is what the project filter leaves
	// of it, which the project pane lists and its cursor indexes.
	allProjects        []model.ProjectWithItemCount
	projects           []model.ProjectWithItemCount
	items              []model.ProjectItemInProject
	groups             []allItemGroup
	blockedSet         map[string]bool
	itemBlockers       map[string][]model.ProjectItem
	itemFilings        map[string][]filing
	hasIncompleteTasks map[string]bool
	taskCounts         map[string][2]int // item ID → [completed, total]
	itemTasks          map[string][]model.ProjectItemTask

	// rows is the item pane's cursor stops: group headers, items and their tasks.
	rows []paneRow
	// folded holds each group folded under its header, by project ID, and opened each item
	// whose detail shows in place, by item ID. details is each such item's detail as last read.
	folded  map[string]bool
	opened  map[string]bool
	details map[string]itemDetailMsg
	// scrolled says ctrl+d, ctrl+u, J or K moved the item pane through an open note, and the
	// pane holds that offset until the cursor moves.
	scrolled bool
	// cursorReset says the next item read lands at the top rather than on the row the cursor
	// was on, because it lists another project.
	cursorReset bool

	// Targets of the prompts, captured on the keypress, because a read landing while one is
	// open may reorder the lists under it.
	addTaskItemID    string
	addTaskItemTitle string
	renameItemID     string
	promptProject    model.ProjectWithItemCount

	activePane    pane
	projectCursor int
	rowCursor     int
	projectScroll int
	rowScroll     int // the item pane's first line on screen

	width  int
	height int

	appMode      appMode
	returnMode   appMode // mode to return to after overlay closes
	titleInput   textinput.Model
	picker       projectPicker
	pendingTitle string // for A flow: title input → project picker
	statusMsg    string // flash feedback in status bar
	flashSeq     int    // tags each flash, so only its own timer clears it

	// The filters / types: the project list's, the item list's, and the dependency map's,
	// which depTree holds.
	projectQuery string
	itemQuery    string
	filterInput  textinput.Model

	// reader is gotermboard's full-screen reader over one item or project while it shows.
	reader   *gotermboard.Reader
	palette  gotermstyle.Palette
	markdown *markdown.Renderer

	depTree depTreeState

	filter filterMode

	// The item a move has grabbed, as an index into m.items, and the project, into m.projects.
	moveItemPos    int
	moveProjectPos int

	// Dependency linking
	depItems      []model.ProjectItem // flat list of selectable items (filtered view)
	depAllItems   []depGroup          // all items grouped by project (source of truth)
	depCursor     int
	depItemID     string
	depItemName   string
	depItemHandle string
	depFilter     textinput.Model
	depFiltering  bool

	showClosed       bool            // include someday, completed and dropped projects
	selectedProjects map[string]bool // the projects space marked, whose items the pane lists

	pendingItemID string // after a read, put the cursor on this item

	errorMsg string
	loading  bool

	where Where
}

// Where names the data the board shows, for its status line: a database on this
// machine, or the API at APIHost. Checkout is where this machine has a repo checked
// out, and whether its registry names a place, for C. A nil Checkout names none.
type Where struct {
	DatabasePath string
	APIHost      string
	Checkout     func(repo string) (path string, named bool, err error)
}

// NewApp creates a new TUI application backed by the given Backend.
func NewApp(b backend.Backend, where Where) *App {
	ti := textinput.New()
	ti.CharLimit = 200
	ti.SetStyles(inputStyles())

	df := textinput.New()
	df.Placeholder = "type to filter..."
	df.CharLimit = 100
	df.SetStyles(inputStyles())

	fi := textinput.New()
	fi.Prompt = "/"
	fi.CharLimit = 100
	fi.SetStyles(inputStyles())

	palette := gotermstyle.NewPalette(true)
	return &App{
		backend:      b,
		blockedSet:   make(map[string]bool),
		itemBlockers: make(map[string][]model.ProjectItem),
		folded:       make(map[string]bool),
		opened:       make(map[string]bool),
		details:      make(map[string]itemDetailMsg),
		titleInput:   ti,
		depFilter:    df,
		filterInput:  fi,
		palette:      palette,
		markdown:     markdown.New(nil),
		where:        where,
	}
}

// OpenOn starts the board on the view --view names. A name ViewNames does not hold changes nothing.
func (m *App) OpenOn(name string) {
	at := slices.Index(ViewNames(), name)
	if at < 0 {
		return
	}
	m.view = view(at)
	if m.view == viewItems {
		m.activePane = itemPane
	}
}

// ViewName is the name of the view showing, as --view takes it.
func (m *App) ViewName() string { return views[m.view].name }

// depGroup holds items belonging to a single project, for display in the dep overlay.
type depGroup struct {
	projectName string
	items       []model.ProjectItem
}

// allItemGroup holds the items of one project in a grouped view.
type allItemGroup struct {
	projectName string
	projectID   string
	startIndex  int // index into the flat m.items slice where this group starts
}

func flattenDepGroups(groups []depGroup) []model.ProjectItem {
	var flat []model.ProjectItem
	for _, g := range groups {
		flat = append(flat, g.items...)
	}
	return flat
}

func filterDepGroups(groups []depGroup, query string) []depGroup {
	var result []depGroup
	for _, g := range groups {
		var items []model.ProjectItem
		for _, item := range g.items {
			if matches(query, item.Title) {
				items = append(items, item)
			}
		}
		if len(items) > 0 {
			result = append(result, depGroup{projectName: g.projectName, items: items})
		}
	}
	return result
}

// --- Messages ---

type projectsMsg []model.ProjectWithItemCount

type itemsMsg struct {
	items              []model.ProjectItemInProject
	blocked            map[string]bool
	blockers           map[string][]model.ProjectItem
	filings            map[string][]filing
	hasIncompleteTasks map[string]bool
	taskCounts         map[string][2]int // item ID → [completed, total]
	itemTasks          map[string][]model.ProjectItemTask
}

type (
	itemCreatedMsg       struct{}
	itemUpdatedMsg       struct{}
	projectCreatedMsg    struct{}
	projectUpdatedMsg    struct{}
	undoResultMsg        string
	membershipUpdatedMsg struct{}
	itemReorderedMsg     struct{}
	projectReorderedMsg  struct{}
	depLinkedMsg         struct{}
	depUnlinkedMsg       struct{}
)

// itemProjectsMsg carries the item whose membership is being managed.
type itemProjectsMsg struct {
	item     model.ProjectItem
	projects []model.Project
}

type itemDetailMsg struct {
	opening      detailOpening
	detail       *model.ProjectItemDetail
	blockers     []model.ProjectItem
	projectNames map[string][]string
	dependents   []model.ProjectItem
	tasks        []model.ProjectItemTask
}

type (
	taskCreatedMsg struct{}
	taskUpdatedMsg struct{}
	taskDeletedMsg struct{}
)

type allItemsMsg struct {
	itemsMsg
	groups []allItemGroup
}

type depCandidatesMsg []depGroup

type depBlockersForUnlinkMsg []model.ProjectItem

type projectDetailMsg struct {
	project *model.ProjectWithItemCount
}

type errMsg struct{ error }

// clearStatusMsg ends the flash whose flashSeq it carries. A newer flash has
// moved flashSeq on, so an older flash's timer leaves it on screen.
type clearStatusMsg struct{ seq int }

// rereadTickMsg reads the API again, since another client may have written.
type rereadTickMsg struct{}

// apiRereadInterval is how stale the board may grow through an API.
const apiRereadInterval = 2 * time.Minute

const statusFlashDuration = 3 * time.Second

func (m *App) flash(msg string) tea.Cmd {
	m.statusMsg = msg
	m.flashSeq++
	seq := m.flashSeq
	return tea.Tick(statusFlashDuration, func(_ time.Time) tea.Msg {
		return clearStatusMsg{seq: seq}
	})
}

// itemHandle names an item the way it is typed on the command line: its number,
// or the UUID tail for an item that has none, which a database once kept as a
// copy of an API may still hold.
func itemHandle(item model.ProjectItem) string {
	if item.Number != nil {
		return strconv.Itoa(*item.Number)
	}
	return shortID(item.ID)
}

// shortID returns the last 8 characters of a UUID for display. Projects and
// tasks have no number of their own, so it is still their handle. UUIDv7
// front-loads the 48-bit millisecond timestamp, so any prefix is identical for
// everything created in the same ~65s window. The entropy is in the tail.
func shortID(id string) string {
	if len(id) >= 8 {
		return id[len(id)-8:]
	}
	return id
}

func truncate(s string, maxWidth int) string {
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}
	if maxWidth <= 1 {
		return "…"
	}
	return string(runes[:maxWidth-1]) + "…"
}

func rereadTickCmd() tea.Cmd {
	return tea.Tick(apiRereadInterval, func(_ time.Time) tea.Msg {
		return rereadTickMsg{}
	})
}

// projectStatusFilter is what the project pane is currently asking for. Closing
// a project is what takes it out of the list, so the default hides the terminal
// ones and `.` is how you get at them.
func (m *App) projectStatusFilter() string {
	if m.showClosed {
		return model.StatusAll
	}
	return model.StatusActive
}

// safeToReread reports whether a background read can land without pulling the
// ground out from under the user. A read replaces the lists and their order,
// which would corrupt a grab in progress or a prompt aimed at a row. Every other
// mode defers to the next tick.
func (m *App) safeToReread() bool {
	return m.appMode == modeNormal && m.reader == nil
}

// --- Commands ---

// fetchProjectsCmd loads the project pane. The status filter is threaded through
// rather than read from the model because every refresh runs as a command off
// the model's goroutine, and a toggle mid-flight would otherwise decide which
// list a already-issued fetch returns.
func fetchProjectsCmd(b backend.Backend, status string) tea.Cmd {
	return func() tea.Msg {
		projects, err := b.ListProjectsByStatus(status)
		if err != nil {
			return errMsg{err}
		}
		return projectsMsg(projects)
	}
}

// setProjectStatusCmd runs a project transition. A drop without a reason is
// refused by the backend, so the flash carries that message rather than the
// caller pre-checking it.
func setProjectStatusCmd(b backend.Backend, id, status string, reason *string) tea.Cmd {
	return func() tea.Msg {
		if _, err := b.SetProjectStatus(id, status, reason); err != nil {
			return errMsg{err}
		}
		return projectUpdatedMsg{}
	}
}

// filingsOf is every item's projects, by ID and name, in two reads. A row names the other
// projects its item is filed in, and a blocker the project it sits in.
func filingsOf(b backend.Backend) (map[string][]filing, error) {
	memberships, err := b.ListMemberships()
	if err != nil {
		return nil, err
	}
	projects, err := b.ListProjectsByStatus(model.StatusAll)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(projects))
	for _, p := range projects {
		names[p.ID] = p.Name
	}
	filed := make(map[string][]filing)
	for _, ms := range memberships {
		filed[ms.ItemID] = append(filed[ms.ItemID], filing{projectID: ms.ProjectID, name: names[ms.ProjectID]})
	}
	return filed, nil
}

// listContext is what every listed item draws beside its own fields: whether it is blocked
// and by what, where it is filed, and its tasks.
func listContext(b backend.Backend, items []model.ProjectItemInProject) (itemsMsg, error) {
	blockedItems, err := b.ListBlocked()
	if err != nil {
		return itemsMsg{}, err
	}
	blockedSet := make(map[string]bool)
	for _, bi := range blockedItems {
		blockedSet[bi.ID] = true
	}
	blockers := make(map[string][]model.ProjectItem)
	for _, item := range items {
		if blockedSet[item.ID] {
			bs, err := b.GetBlockers(item.ID)
			if err != nil {
				return itemsMsg{}, err
			}
			blockers[item.ID] = bs
		}
	}
	filings, err := filingsOf(b)
	if err != nil {
		return itemsMsg{}, err
	}
	allTasks, taskCounts, hasIncompleteTasks, err := taskSummary(b, items)
	if err != nil {
		return itemsMsg{}, err
	}
	return itemsMsg{
		items: items, blocked: blockedSet, blockers: blockers, filings: filings,
		hasIncompleteTasks: hasIncompleteTasks, taskCounts: taskCounts, itemTasks: allTasks,
	}, nil
}

func fetchItemsCmd(b backend.Backend, projectID string, filter filterMode) tea.Cmd {
	return func() tea.Msg {
		items, err := b.ListItemsByProject(projectID)
		if err != nil {
			return errMsg{err}
		}
		if filter == filterAll {
			archived, err := b.ListArchived(projectID)
			if err != nil {
				return errMsg{err}
			}
			items = append(items, archived...)
		}
		msg, err := listContext(b, items)
		if err != nil {
			return errMsg{err}
		}
		if filter == filterBlocked {
			msg.items = slices.DeleteFunc(msg.items, func(item model.ProjectItemInProject) bool { return !msg.blocked[item.ID] })
		}
		return msg
	}
}

// taskSummary is each listed item's tasks, its done and total counts, and
// whether any task is still open, from one read of every task.
func taskSummary(b backend.Backend, items []model.ProjectItemInProject) (
	tasks map[string][]model.ProjectItemTask, counts map[string][2]int, open map[string]bool, err error,
) {
	every, err := b.ListAllTasks()
	if err != nil {
		return nil, nil, nil, err
	}
	listed := make(map[string]bool, len(items))
	for _, item := range items {
		listed[item.ID] = true
	}
	tasks = make(map[string][]model.ProjectItemTask)
	for _, task := range every {
		if listed[task.ItemID] {
			tasks[task.ItemID] = append(tasks[task.ItemID], task)
		}
	}
	counts = make(map[string][2]int, len(tasks))
	open = make(map[string]bool)
	for id, held := range tasks {
		done := 0
		for _, task := range held {
			if task.Completed {
				done++
			}
		}
		counts[id] = [2]int{done, len(held)}
		if done < len(held) {
			open[id] = true
		}
	}
	return tasks, counts, open, nil
}

func fetchAllItemsCmd(b backend.Backend, projects []model.ProjectWithItemCount, filter filterMode) tea.Cmd {
	return func() tea.Msg {
		var groups []allItemGroup
		var allItems []model.ProjectItemInProject
		for _, p := range projects {
			items, err := b.ListItemsByProject(p.ID)
			if err != nil {
				return errMsg{err}
			}
			if filter == filterAll {
				archived, err := b.ListArchived(p.ID)
				if err != nil {
					return errMsg{err}
				}
				items = append(items, archived...)
			}
			if len(items) > 0 {
				groups = append(groups, allItemGroup{projectName: p.Name, projectID: p.ID, startIndex: len(allItems)})
				allItems = append(allItems, items...)
			}
		}
		msg, err := listContext(b, allItems)
		if err != nil {
			return errMsg{err}
		}
		if filter == filterBlocked {
			var kept []model.ProjectItemInProject
			var keptGroups []allItemGroup
			for gi, g := range groups {
				end := len(allItems)
				if gi+1 < len(groups) {
					end = groups[gi+1].startIndex
				}
				start := len(kept)
				for _, item := range allItems[g.startIndex:end] {
					if msg.blocked[item.ID] {
						kept = append(kept, item)
					}
				}
				if len(kept) > start {
					keptGroups = append(keptGroups, allItemGroup{projectName: g.projectName, projectID: g.projectID, startIndex: start})
				}
			}
			msg.items, groups = kept, keptGroups
		}
		return allItemsMsg{itemsMsg: msg, groups: groups}
	}
}

func createItemCmd(b backend.Backend, input model.CreateProjectItem) tea.Cmd {
	return func() tea.Msg {
		if _, err := b.CreateItem(input); err != nil {
			return errMsg{err}
		}
		return itemCreatedMsg{}
	}
}

func updateItemCmd(b backend.Backend, id string, input model.UpdateProjectItem) tea.Cmd {
	return func() tea.Msg {
		if _, err := b.UpdateItem(id, input); err != nil {
			return errMsg{err}
		}
		return itemUpdatedMsg{}
	}
}

func createProjectCmd(b backend.Backend, input model.CreateProject) tea.Cmd {
	return func() tea.Msg {
		if _, err := b.CreateProject(input); err != nil {
			return errMsg{err}
		}
		return projectCreatedMsg{}
	}
}

func updateProjectCmd(b backend.Backend, id string, input model.UpdateProject) tea.Cmd {
	return func() tea.Msg {
		if _, err := b.UpdateProject(id, input); err != nil {
			return errMsg{err}
		}
		return projectUpdatedMsg{}
	}
}

func fetchProjectDetailCmd(b backend.Backend, projectID string) tea.Cmd {
	return func() tea.Msg {
		p, err := b.GetProject(projectID)
		if err != nil {
			return errMsg{err}
		}
		return projectDetailMsg{project: p}
	}
}

func undoCmd(b backend.Backend) tea.Cmd {
	return func() tea.Msg {
		result, err := b.Undo()
		if err != nil {
			return errMsg{err}
		}
		return undoResultMsg(result.Description)
	}
}

func fetchItemProjectsCmd(b backend.Backend, item model.ProjectItem) tea.Cmd {
	return func() tea.Msg {
		projects, err := b.GetItemProjects(item.ID)
		if err != nil {
			return errMsg{err}
		}
		return itemProjectsMsg{item: item, projects: projects}
	}
}

func updateMembershipCmd(b backend.Backend, itemID string, toAdd, toRemove []string) tea.Cmd {
	return func() tea.Msg {
		for _, pid := range toAdd {
			if err := b.AddToProject(itemID, pid); err != nil {
				return errMsg{err}
			}
		}
		for _, pid := range toRemove {
			if err := b.RemoveFromProject(itemID, pid); err != nil {
				return errMsg{err}
			}
		}
		return membershipUpdatedMsg{}
	}
}

func fetchItemDetailCmd(b backend.Backend, itemID string, isBlocked bool, opening detailOpening) tea.Cmd {
	return func() tea.Msg {
		detail, err := b.GetItem(itemID)
		if err != nil {
			return errMsg{err}
		}
		var blockers []model.ProjectItem
		pnames := make(map[string][]string)
		if isBlocked {
			blockers, err = b.GetBlockers(itemID)
			if err != nil {
				return errMsg{err}
			}
			for _, bl := range blockers {
				ps, err := b.GetItemProjects(bl.ID)
				if err != nil {
					return errMsg{err}
				}
				var names []string
				for _, p := range ps {
					names = append(names, p.Name)
				}
				pnames[bl.ID] = names
			}
		}
		// GetBlockers answers only the other direction, so what this item
		// blocks is read off the whole edge list.
		edges, err := b.ListDependencies()
		if err != nil {
			return errMsg{err}
		}
		var dependents []model.ProjectItem
		for _, e := range edges {
			if e.DependsOnID != itemID {
				continue
			}
			dep, err := b.GetItem(e.ItemID)
			if err != nil {
				return errMsg{err}
			}
			dependents = append(dependents, dep.ProjectItem)
		}
		tasks, err := b.ListTasks(itemID)
		if err != nil {
			return errMsg{err}
		}
		return itemDetailMsg{opening: opening, detail: detail, blockers: blockers, projectNames: pnames, dependents: dependents, tasks: tasks}
	}
}

func createTaskCmd(b backend.Backend, itemID, title string) tea.Cmd {
	return func() tea.Msg {
		if _, err := b.CreateTask(itemID, model.CreateProjectItemTask{Title: title}); err != nil {
			return errMsg{err}
		}
		return taskCreatedMsg{}
	}
}

func toggleTaskCmd(b backend.Backend, itemID string, task model.ProjectItemTask) tea.Cmd {
	return func() tea.Msg {
		toggled := !task.Completed
		if _, err := b.UpdateTask(itemID, task.ID, model.UpdateProjectItemTask{Completed: &toggled}); err != nil {
			return errMsg{err}
		}
		return taskUpdatedMsg{}
	}
}

func deleteTaskCmd(b backend.Backend, itemID, taskID string) tea.Cmd {
	return func() tea.Msg {
		if err := b.DeleteTask(itemID, taskID); err != nil {
			return errMsg{err}
		}
		return taskDeletedMsg{}
	}
}

// reorderItemCmd lands an item just below above, or first with no row above it. A
// position counts every member of the project, so the archived rows this list hides
// count toward it.
func reorderItemCmd(b backend.Backend, itemID, projectID string, above *model.ProjectItemInProject) tea.Cmd {
	return func() tea.Msg {
		position := 0
		if above != nil {
			unarchived, err := b.ListItemsByProject(projectID)
			if err != nil {
				return errMsg{err}
			}
			archived, err := b.ListArchived(projectID)
			if err != nil {
				return errMsg{err}
			}
			position = positionBelow(slices.Concat(unarchived, archived), itemID, above.ID)
		}
		if err := b.ReorderItem(itemID, projectID, position); err != nil {
			return errMsg{err}
		}
		return itemReorderedMsg{}
	}
}

// positionBelow counts the members ahead of a drop, in the order a store lists them,
// because a row tied on position with aboveID can sit on either side of it.
func positionBelow(members []model.ProjectItemInProject, itemID, aboveID string) int {
	slices.SortStableFunc(members, func(a, b model.ProjectItemInProject) int {
		return cmp.Or(
			cmp.Compare(a.Position, b.Position),
			a.CreatedAt.Compare(b.CreatedAt),
			strings.Compare(a.ID, b.ID),
		)
	})
	position := 0
	for _, member := range members {
		if member.ID == itemID {
			continue
		}
		position++
		if member.ID == aboveID {
			break
		}
	}
	return position
}

func reorderProjectCmd(b backend.Backend, projectID string, newPos int) tea.Cmd {
	return func() tea.Msg {
		if err := b.ReorderProject(projectID, newPos); err != nil {
			return errMsg{err}
		}
		return projectReorderedMsg{}
	}
}

func fetchDepCandidatesCmd(b backend.Backend, projects []model.ProjectWithItemCount) tea.Cmd {
	return func() tea.Msg {
		var groups []depGroup
		for _, p := range projects {
			items, err := b.ListItemsByProject(p.ID)
			if err != nil {
				return errMsg{err}
			}
			var plain []model.ProjectItem
			for _, it := range items {
				plain = append(plain, it.ProjectItem)
			}
			if len(plain) > 0 {
				groups = append(groups, depGroup{projectName: p.Name, items: plain})
			}
		}
		return depCandidatesMsg(groups)
	}
}

func fetchDepBlockersCmd(b backend.Backend, itemID string) tea.Cmd {
	return func() tea.Msg {
		blockers, err := b.GetBlockers(itemID)
		if err != nil {
			return errMsg{err}
		}
		return depBlockersForUnlinkMsg(blockers)
	}
}

func addDependencyCmd(b backend.Backend, itemID, dependsOn string) tea.Cmd {
	return func() tea.Msg {
		if err := b.AddDependency(itemID, dependsOn); err != nil {
			return errMsg{err}
		}
		return depLinkedMsg{}
	}
}

func removeDependencyCmd(b backend.Backend, itemID, dependsOn string) tea.Cmd {
	return func() tea.Msg {
		if err := b.RemoveDependency(itemID, dependsOn); err != nil {
			return errMsg{err}
		}
		return depUnlinkedMsg{}
	}
}

// --- Bubble Tea interface ---

// fetchItems reads what the item pane lists: every project's items in All items, the
// marked projects' while any is marked, or else the project under the cursor's. It is nil
// with no project to read.
func (m *App) fetchItems() tea.Cmd {
	switch {
	case m.view == viewItems:
		return fetchAllItemsCmd(m.backend, m.allProjects, m.filter)
	case len(m.selectedProjects) > 0:
		var selected []model.ProjectWithItemCount
		for _, p := range m.allProjects {
			if m.selectedProjects[p.ID] {
				selected = append(selected, p)
			}
		}
		return fetchAllItemsCmd(m.backend, selected, m.filter)
	case m.projectCursor < len(m.projects):
		return fetchItemsCmd(m.backend, m.projects[m.projectCursor].ID, m.filter)
	}
	return nil
}

// isGroupedView says the item pane lists several projects' items, each under its header.
func (m *App) isGroupedView() bool {
	return m.view == viewItems || (m.view == viewProjects && len(m.selectedProjects) > 0)
}

// rereadCmd reads the projects again, and the dependency map with them where it shows.
func (m *App) rereadCmd() tea.Cmd {
	cmd := fetchProjectsCmd(m.backend, m.projectStatusFilter())
	if m.view == viewDependencies {
		return tea.Batch(cmd, fetchDepTreeCmd(m.backend, m.depTree.forestID()))
	}
	return cmd
}

func (m *App) Init() tea.Cmd {
	cmds := []tea.Cmd{fetchProjectsCmd(m.backend, m.projectStatusFilter())}
	if m.view == viewDependencies {
		cmds = append(cmds, fetchDepTreeCmd(m.backend, ""))
	}
	if m.where.APIHost != "" {
		cmds = append(cmds, rereadTickCmd())
	}
	return tea.Batch(cmds...)
}

func (m *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.reader != nil {
			m.reader.Update(msg)
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(sameKeyOnEveryTerminal(msg))

	case tea.PasteMsg:
		if m.reader != nil {
			_, cmd := m.reader.Update(msg)
			return m, cmd
		}
		cmd := m.pasteIntoFocusedInput(msg)
		return m, cmd

	case projectsMsg:
		keep := ""
		if m.projectCursor < len(m.projects) {
			keep = m.projects[m.projectCursor].ID
		}
		m.allProjects = msg
		m.narrowProjects(keep)
		if m.view == viewDependencies {
			return m, nil
		}
		if cmd := m.fetchItems(); cmd != nil {
			return m, cmd
		}
		m.items, m.groups, m.rows, m.rowCursor = nil, nil, nil, 0
		return m, nil

	case allItemsMsg:
		m.takeItems(msg.itemsMsg, msg.groups)
		cmd := m.readOpenDetailsAgain()
		return m, cmd

	case itemsMsg:
		m.takeItems(msg, nil)
		cmd := m.readOpenDetailsAgain()
		return m, cmd

	case taskCreatedMsg, taskUpdatedMsg, taskDeletedMsg:
		cmd := m.fetchItems()
		return m, cmd

	case itemCreatedMsg:
		flashCmd := m.flash("Item created")
		return m, tea.Batch(fetchProjectsCmd(m.backend, m.projectStatusFilter()), flashCmd)

	case itemUpdatedMsg:
		var flashCmd tea.Cmd
		if m.statusMsg == "" {
			flashCmd = m.flash("Item updated")
		}
		return m, tea.Batch(fetchProjectsCmd(m.backend, m.projectStatusFilter()), flashCmd)

	case externalEditDoneMsg:
		m.appMode = m.returnMode
		saveCmd, note := finishExternalEdit(m.backend, msg)
		if note != "" {
			cmd := m.flash(note)
			return m, cmd
		}
		return m, saveCmd

	case gotermboard.ClaudeSessionOutcome:
		cmd := m.tookClaudeOutcome(msg)
		return m, cmd

	case projectCreatedMsg:
		flashCmd := m.flash("Project created")
		return m, tea.Batch(fetchProjectsCmd(m.backend, m.projectStatusFilter()), flashCmd)

	case projectUpdatedMsg:
		flashCmd := m.flash("Project updated")
		return m, tea.Batch(fetchProjectsCmd(m.backend, m.projectStatusFilter()), flashCmd)

	case projectDetailMsg:
		m.openReader(msg.project.Name, m.projectDocument(*msg.project))
		return m, nil

	case itemDetailMsg:
		m.tookDetail(msg)
		return m, nil

	case undoResultMsg:
		flashCmd := m.flash(fmt.Sprintf("Undo: %s", string(msg)))
		return m, tea.Batch(m.rereadCmd(), flashCmd)

	case depTreeMsg:
		m.depTree.tree = msg.tree
		m.depTree.lookup = msg.lookup
		m.depTree.rebuildForest()
		// Opening on an item puts the forest cursor there, so the map lands on what you were
		// looking at. Opening cold starts at the top.
		var missCmd tea.Cmd
		if msg.focusID != "" && !m.depTree.standOn(msg.focusID) && m.view == viewDependencies {
			missCmd = m.flash(m.depTreeMiss(msg.focusID))
		}
		m.depTree.focusOn(m.depTree.forestID())
		return m, missCmd

	case itemProjectsMsg:
		m.picker = newPickerForManage(m.allProjects, msg.projects, msg.item)
		m.returnMode = modeNormal
		m.appMode = modeProjectPicker
		return m, nil

	case membershipUpdatedMsg:
		flashCmd := m.flash("Project membership updated")
		return m, tea.Batch(fetchProjectsCmd(m.backend, m.projectStatusFilter()), flashCmd)

	case itemReorderedMsg:
		flashCmd := m.flash("Item reordered")
		if cmd := m.fetchItems(); cmd != nil {
			return m, tea.Batch(cmd, flashCmd)
		}
		return m, flashCmd

	case projectReorderedMsg:
		flashCmd := m.flash("Project reordered")
		return m, tea.Batch(fetchProjectsCmd(m.backend, m.projectStatusFilter()), flashCmd)

	case depCandidatesMsg:
		// The item cannot depend on itself, so it leaves its own list.
		var groups []depGroup
		for _, g := range msg {
			items := slices.DeleteFunc(slices.Clone(g.items), func(item model.ProjectItem) bool { return item.ID == m.depItemID })
			if len(items) > 0 {
				groups = append(groups, depGroup{projectName: g.projectName, items: items})
			}
		}
		m.depAllItems = groups
		m.depFilter.SetValue("")
		m.depFilter.Blur()
		m.depFiltering = false
		m.depItems = flattenDepGroups(groups)
		m.depCursor = 0
		m.appMode = modeDepLink
		return m, nil

	case depBlockersForUnlinkMsg:
		if len(msg) == 0 {
			cmd := m.flash("No dependencies to unlink")
			return m, cmd
		}
		m.depItems = msg
		m.depCursor = 0
		m.appMode = modeDepUnlink
		return m, nil

	case depLinkedMsg:
		flashCmd := m.flash("Dependency linked")
		return m, tea.Batch(fetchProjectsCmd(m.backend, m.projectStatusFilter()), flashCmd)

	case depUnlinkedMsg:
		flashCmd := m.flash("Dependency unlinked")
		return m, tea.Batch(fetchProjectsCmd(m.backend, m.projectStatusFilter()), flashCmd)

	case clearStatusMsg:
		if msg.seq == m.flashSeq {
			m.statusMsg = ""
		}
		return m, nil

	case errMsg:
		m.loading = false
		m.errorMsg = msg.Error()
		return m, nil

	case rereadTickMsg:
		// A tick re-arms even when it skips the read. One that returned nothing
		// would end the rereads for the rest of the session.
		next := rereadTickCmd()
		if !m.safeToReread() {
			return m, next
		}
		return m, tea.Batch(fetchProjectsCmd(m.backend, m.projectStatusFilter()), next)
	}

	if m.reader != nil {
		_, cmd := m.reader.Update(msg)
		return m, cmd
	}
	return m, nil
}

// takeItems lands a read of the item pane's items, keeping the cursor on the row it was on.
func (m *App) takeItems(msg itemsMsg, groups []allItemGroup) {
	keep := ""
	if !m.cursorReset {
		keep = m.cursorKey()
	}
	m.cursorReset = false
	m.items = msg.items
	m.groups = groups
	m.blockedSet = msg.blocked
	m.itemBlockers = msg.blockers
	m.itemFilings = msg.filings
	m.hasIncompleteTasks = msg.hasIncompleteTasks
	m.taskCounts = msg.taskCounts
	m.itemTasks = msg.itemTasks
	m.rebuildRows(keep)
	if m.pendingItemID != "" {
		if at := slices.IndexFunc(m.items, func(item model.ProjectItemInProject) bool { return item.ID == m.pendingItemID }); at >= 0 {
			m.rowCursor = m.firstRowForItem(at)
		}
		m.pendingItemID = ""
	}
}

// narrowProjects lists what the project filter keeps of every project read, and keeps the
// cursor on keep while it still shows.
func (m *App) narrowProjects(keep string) {
	m.projects = slices.DeleteFunc(slices.Clone(m.allProjects), func(p model.ProjectWithItemCount) bool {
		description := ""
		if p.Description != nil {
			description = *p.Description
		}
		return !matches(m.projectQuery, p.Name, description)
	})
	if at := slices.IndexFunc(m.projects, func(p model.ProjectWithItemCount) bool { return p.ID == keep }); at >= 0 {
		m.projectCursor = at
	}
	m.projectCursor = max(0, min(m.projectCursor, len(m.projects)-1))
}

// --- Key handling ---

// sameKeyOnEveryTerminal folds the keys only some terminals report apart. Bubble Tea
// asks every terminal to separate ctrl+[ from Esc, ctrl+m from Enter and ctrl+i from
// Tab, and one that cannot sends the same bytes for each pair.
func sameKeyOnEveryTerminal(msg tea.KeyPressMsg) tea.KeyPressMsg {
	switch msg.String() {
	case "ctrl+[":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+m":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "ctrl+i":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	}
	return msg
}

// pasteIntoFocusedInput flattens a paste onto the one line every input here holds,
// so a copied line's newline cannot defeat a filter, and sends it where typing goes.
// With no input focused a paste only clears the transient message, as a key does.
func (m *App) pasteIntoFocusedInput(msg tea.PasteMsg) tea.Cmd {
	m.errorMsg = ""
	msg.Content = strings.Join(strings.Fields(msg.Content), " ")
	return m.updateFocusedInput(msg)
}

// updateFocusedInput sends a key or a paste to the input this mode types into.
func (m *App) updateFocusedInput(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch {
	case m.appMode == modeAddItem, m.appMode == modeAddItemMulti, m.appMode == modeAddProject,
		m.appMode == modeAddTask, m.appMode == modeRenameItem, m.appMode == modeRenameProject,
		m.appMode == modeDropProject:
		m.titleInput, cmd = m.titleInput.Update(msg)
	case m.appMode == modeFilter:
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.applyQuery(m.filterInput.Value())
	case (m.appMode == modeDepLink || m.appMode == modeDepUnlink) && m.depFiltering:
		cmd = m.updateDepFilter(msg)
	}
	return cmd
}

func (m *App) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.reader != nil {
		_, cmd := m.reader.Update(msg)
		return m, cmd
	}
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	m.errorMsg = ""
	cmd, bound := m.runKey(msg.String())
	if !bound && m.typesText() {
		cmd = m.typeKey(msg)
	}
	return m, cmd
}

// typesText says the screen has a text box, which takes every key its table leaves free.
func (m *App) typesText() bool {
	switch m.appMode {
	case modeAddItem, modeAddItemMulti, modeAddProject, modeAddTask, modeRenameItem, modeRenameProject, modeDropProject, modeFilter:
		return true
	case modeDepLink, modeDepUnlink:
		return m.depFiltering
	}
	return false
}

// typeKey sends a key to the text box. The filter narrows the project list as it is typed, so
// the item pane follows the project the cursor lands on.
func (m *App) typeKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.appMode != modeFilter {
		return m.updateFocusedInput(msg)
	}
	before := m.cursorProjectID()
	cmd := m.updateFocusedInput(msg)
	return tea.Batch(cmd, m.refetchAfterProjectFilter(before))
}

// cursorProjectID is the project under the project pane's cursor, or "" with none.
func (m *App) cursorProjectID() string {
	if m.projectCursor < len(m.projects) {
		return m.projects[m.projectCursor].ID
	}
	return ""
}

// confirmPrompt is Enter in a prompt: it does what the prompt asks with what was typed, and
// nothing while the box is empty.
func (m *App) confirmPrompt() tea.Cmd {
	value := strings.TrimSpace(m.titleInput.Value())
	if value == "" {
		return nil
	}
	m.titleInput.Blur()
	asked := m.appMode
	m.appMode = modeNormal
	switch asked {
	case modeAddItem:
		projectID := m.projects[m.projectCursor].ID
		return createItemCmd(m.backend, model.CreateProjectItem{Title: value, ProjectIDs: []string{projectID}})
	case modeAddItemMulti:
		m.pendingTitle = value
		currentProjectID := ""
		if !m.isGroupedView() {
			currentProjectID = m.cursorProjectID()
		}
		m.picker = newPickerForCreate(m.allProjects, currentProjectID, value)
		m.appMode = modeProjectPicker
	case modeAddProject:
		return createProjectCmd(m.backend, model.CreateProject{Name: value})
	case modeAddTask:
		return createTaskCmd(m.backend, m.addTaskItemID, value)
	case modeRenameItem:
		return updateItemCmd(m.backend, m.renameItemID, model.UpdateProjectItem{Title: &value})
	case modeRenameProject:
		return updateProjectCmd(m.backend, m.promptProject.ID, model.UpdateProject{Name: &value})
	case modeDropProject:
		return setProjectStatusCmd(m.backend, m.promptProject.ID, model.StatusDropped, &value)
	}
	return nil
}

func (m *App) cancelPrompt() tea.Cmd {
	m.appMode = modeNormal
	m.titleInput.Blur()
	m.pendingTitle = ""
	return nil
}

// confirmPicker files the item in the projects ticked, or creates it there.
func (m *App) confirmPicker() tea.Cmd {
	m.appMode = modeNormal
	switch m.picker.intent {
	case pickerCreate:
		m.pendingTitle = ""
		return createItemCmd(m.backend, model.CreateProjectItem{Title: m.picker.itemTitle, ProjectIDs: m.picker.selectedIDs()})
	case pickerManage:
		toAdd, toRemove := m.picker.toAdd(), m.picker.toRemove()
		if len(toAdd) == 0 && len(toRemove) == 0 {
			return nil
		}
		return updateMembershipCmd(m.backend, m.picker.itemID, toAdd, toRemove)
	}
	return nil
}

func (m *App) cancelPicker() tea.Cmd {
	m.appMode = modeNormal
	m.pendingTitle = ""
	return nil
}

func (m *App) closeHelp() tea.Cmd {
	m.appMode = m.returnMode
	return nil
}

// --- Views ---

// switchView shows v. The dependency map opens standing on focusID, or where it stood last.
func (m *App) switchView(v view, focusID string) tea.Cmd {
	if item := m.currentItem(); item != nil {
		m.pendingItemID = item.ID
	}
	m.view = v
	m.scrolled = false
	m.cursorReset = false
	switch v {
	case viewDependencies:
		if focusID == "" {
			focusID = m.depTree.forestID()
		}
		m.depTree = depTreeState{showFinished: m.depTree.showFinished, query: m.depTree.query}
		return fetchDepTreeCmd(m.backend, focusID)
	case viewItems:
		m.activePane = itemPane
	}
	return m.fetchItems()
}

func (m *App) cycleView(direction int) tea.Cmd {
	return m.switchView(view((int(m.view)+direction+len(views))%len(views)), "")
}

// readAgain is R: every list the board shows, read again now.
func (m *App) readAgain() tea.Cmd {
	source := m.where.APIHost
	if source == "" {
		source = m.where.DatabasePath
	}
	return tea.Batch(m.rereadCmd(), m.flash("Read again from "+source))
}

func (m *App) setFilter(mode filterMode) tea.Cmd {
	if m.filter == mode {
		mode = filterNone
	}
	m.filter = mode
	return m.fetchItems()
}

func (m *App) toggleClosedProjects() tea.Cmd {
	m.showClosed = !m.showClosed
	// The cursor indexes the list that is about to be replaced, and the shorter list is the
	// one that would panic.
	m.projectCursor = 0
	m.projectScroll = 0
	m.selectedProjects = nil
	note := "Shelved and closed projects hidden"
	if m.showClosed {
		note = "Showing shelved and closed projects"
	}
	return tea.Batch(fetchProjectsCmd(m.backend, m.projectStatusFilter()), m.flash(note))
}

// --- The filter ---

// filterPane is the list the filter is typed for.
func (m *App) filterPane() pane {
	if m.view == viewProjects && m.activePane == projectPane {
		return projectPane
	}
	return itemPane
}

func (m *App) currentQuery() string {
	switch {
	case m.view == viewDependencies:
		return m.depTree.query
	case m.filterPane() == projectPane:
		return m.projectQuery
	}
	return m.itemQuery
}

// startFilter is /: the filter line opens on the list showing, holding what it kept.
func (m *App) startFilter() tea.Cmd {
	m.filterInput.SetValue(m.currentQuery())
	m.filterInput.CursorEnd()
	m.appMode = modeFilter
	return m.filterInput.Focus()
}

// applyQuery narrows the list the filter is typed for to query, as each key lands.
func (m *App) applyQuery(query string) {
	if query == m.currentQuery() {
		return
	}
	switch {
	case m.view == viewDependencies:
		s := &m.depTree
		s.query = query
		s.forestCursor, s.forestScroll = 0, 0
		s.rebuildForest()
		s.focusOn(s.forestID())
	case m.filterPane() == projectPane:
		keep := ""
		if m.projectCursor < len(m.projects) {
			keep = m.projects[m.projectCursor].ID
		}
		m.projectQuery = query
		m.narrowProjects(keep)
	default:
		keep := m.cursorKey()
		m.itemQuery = query
		m.scrolled = false
		m.rebuildRows(keep)
	}
}

// refetchAfterProjectFilter reads the items of the project the narrowed list put the cursor
// on, where that is another project than before.
func (m *App) refetchAfterProjectFilter(before string) tea.Cmd {
	if m.filterPane() != projectPane || len(m.selectedProjects) > 0 {
		return nil
	}
	if m.projectCursor < len(m.projects) && m.projects[m.projectCursor].ID != before {
		m.cursorReset = true
		return m.fetchItems()
	}
	if len(m.projects) == 0 {
		m.items, m.groups, m.rows = nil, nil, nil
	}
	return nil
}

// keepFilter is Enter while typing a filter: the list stays narrowed and takes the keys again.
func (m *App) keepFilter() tea.Cmd {
	before := m.cursorProjectID()
	m.appMode = modeNormal
	m.filterInput.Blur()
	return m.refetchAfterProjectFilter(before)
}

func (m *App) clearFilter() tea.Cmd {
	before := m.cursorProjectID()
	m.applyQuery("")
	m.appMode = modeNormal
	m.filterInput.Blur()
	return m.refetchAfterProjectFilter(before)
}

// filterLine is the line under a pane's title that the filter takes: the line being typed, or
// the filter kept. It is absent where neither is.
func (m *App) filterLine(p pane) (string, bool) {
	query := m.itemQuery
	if p == projectPane {
		query = m.projectQuery
	}
	switch {
	case m.appMode == modeFilter && m.view != viewDependencies && m.filterPane() == p:
		return " " + inputView(m.filterInput), true
	case query != "":
		return filterIndicatorStyle.Render(" /" + query), true
	}
	return "", false
}

// canBackOut says esc has something to back out of: a filter, a mark, or a drill in the map.
func (m *App) canBackOut() bool {
	switch {
	case m.view == viewDependencies:
		return m.depTree.query != "" || m.depTree.active == focusPane
	case m.filterPane() == projectPane && m.projectQuery != "":
		return true
	case m.filterPane() == itemPane && m.itemQuery != "":
		return true
	}
	return m.view == viewProjects && len(m.selectedProjects) > 0
}

// backOut is esc: it clears the filter, then the marked projects, or backs out of the map's
// tree one drill at a time.
func (m *App) backOut() tea.Cmd {
	before := ""
	if m.projectCursor < len(m.projects) {
		before = m.projects[m.projectCursor].ID
	}
	switch {
	case m.view == viewDependencies && m.depTree.query != "":
		m.applyQuery("")
	case m.view == viewDependencies:
		return m.depTreeLeft()
	case m.filterPane() == projectPane && m.projectQuery != "":
		m.applyQuery("")
		return m.refetchAfterProjectFilter(before)
	case m.filterPane() == itemPane && m.itemQuery != "":
		m.applyQuery("")
	case len(m.selectedProjects) > 0:
		m.selectedProjects = nil
		m.cursorReset = true
		return m.fetchItems()
	}
	return nil
}

// --- Moving the cursor ---

func (m *App) onProject() bool {
	return m.view == viewProjects && m.activePane == projectPane && m.projectCursor < len(m.projects)
}

func (m *App) onItemList() bool {
	return (m.view == viewItems || m.activePane == itemPane) && len(m.rows) > 0
}

// pointAtProject puts the project cursor on at, and reads that project's items unless marked
// projects hold the pane.
func (m *App) pointAtProject(at int) tea.Cmd {
	at = max(0, min(at, len(m.projects)-1))
	if at == m.projectCursor || len(m.projects) == 0 {
		return nil
	}
	m.projectCursor = at
	if len(m.selectedProjects) > 0 {
		return nil
	}
	m.rowCursor, m.rowScroll, m.scrolled, m.cursorReset = 0, 0, false, true
	return m.fetchItems()
}

func (m *App) moveBy(rows int) tea.Cmd {
	if m.view == viewProjects && m.activePane == projectPane {
		return m.pointAtProject(m.projectCursor + rows)
	}
	m.moveCursorTo(m.rowCursor + rows)
	return nil
}

// paneContentHeight is how many lines a pane's list gets, under the header, its border, its
// title and the status bar.
func (m *App) paneContentHeight() int {
	return max(m.height-5, 1)
}

// --- Acting on rows ---

func (m *App) currentProject() model.ProjectWithItemCount { return m.projects[m.projectCursor] }

func (m *App) readProject() tea.Cmd {
	return fetchProjectDetailCmd(m.backend, m.currentProject().ID)
}

func (m *App) toggleProjectMark() tea.Cmd {
	if m.selectedProjects == nil {
		m.selectedProjects = make(map[string]bool)
	}
	p := m.currentProject()
	if m.selectedProjects[p.ID] {
		delete(m.selectedProjects, p.ID)
	} else {
		m.selectedProjects[p.ID] = true
	}
	if len(m.selectedProjects) == 0 {
		m.selectedProjects = nil
	}
	m.rowCursor, m.rowScroll, m.cursorReset = 0, 0, true
	return m.fetchItems()
}

func (m *App) prompt(mode appMode, value, placeholder string) tea.Cmd {
	m.titleInput.SetValue(value)
	m.titleInput.Placeholder = placeholder
	m.appMode = mode
	return m.titleInput.Focus()
}

func (m *App) startAddProject() tea.Cmd {
	return m.prompt(modeAddProject, "", "Enter project name...")
}

func (m *App) startAddItem() tea.Cmd {
	return m.prompt(modeAddItem, "", "Enter item title...")
}

func (m *App) startAddItemToSeveral() tea.Cmd {
	return m.prompt(modeAddItemMulti, "", "Enter item title...")
}

func (m *App) startRenameProject() tea.Cmd {
	m.promptProject = m.currentProject()
	return m.prompt(modeRenameProject, m.promptProject.Name, "")
}

func (m *App) startDropProject() tea.Cmd {
	// A reason is required, so dropping is a prompt rather than a keypress: "deferred" invites
	// the same idea back, "dropped, and here is why" does not.
	m.promptProject = m.currentProject()
	return m.prompt(modeDropProject, "", "Why dropped rather than deferred?")
}

func (m *App) editProjectDescription() tea.Cmd {
	p := m.currentProject()
	desc := ""
	if p.Description != nil {
		desc = *p.Description
	}
	return m.openInEditor(editTarget{projectID: p.ID}, projectDescriptionFileName(p.Project), desc)
}

func (m *App) completeProject() tea.Cmd {
	return setProjectStatusCmd(m.backend, m.currentProject().ID, model.StatusCompleted, nil)
}

// shelveProject asks for no reason, because shelving closes no question about the project.
func (m *App) shelveProject() tea.Cmd {
	return setProjectStatusCmd(m.backend, m.currentProject().ID, model.StatusSomeday, nil)
}

func (m *App) reopenProject() tea.Cmd {
	return setProjectStatusCmd(m.backend, m.currentProject().ID, model.StatusActive, nil)
}

func (m *App) toggleGroup() tea.Cmd {
	row := m.currentRow()
	id := m.groups[row.group].projectID
	m.folded[id] = !m.folded[id]
	m.rebuildRows(m.cursorKey())
	return nil
}

func (m *App) toggleItemDone() tea.Cmd {
	item := m.currentItem()
	if !item.Completed && m.hasIncompleteTasks[item.ID] {
		return m.flash("Cannot complete: item has incomplete tasks")
	}
	if !item.Completed && m.blockedSet[item.ID] {
		return m.flash("Cannot complete: item has unresolved blockers")
	}
	toggled := !item.Completed
	note := "Marked incomplete"
	if toggled {
		note = "Marked done"
	}
	flashCmd := m.flash(note)
	return tea.Batch(updateItemCmd(m.backend, item.ID, model.UpdateProjectItem{Completed: &toggled}), flashCmd)
}

func (m *App) archiveItem() tea.Cmd {
	archived := true
	flashCmd := m.flash("Archived")
	return tea.Batch(updateItemCmd(m.backend, m.currentItem().ID, model.UpdateProjectItem{Archived: &archived}), flashCmd)
}

func (m *App) editItemNote() tea.Cmd {
	item := m.currentItem().ProjectItem
	return m.openInEditor(editTarget{itemID: item.ID}, itemNoteFileName(item), noteOf(item))
}

func (m *App) startRenameItem() tea.Cmd {
	item := m.currentItem()
	m.renameItemID = item.ID
	return m.prompt(modeRenameItem, item.Title, "")
}

func (m *App) manageItemProjects() tea.Cmd {
	return fetchItemProjectsCmd(m.backend, m.currentItem().ProjectItem)
}

func (m *App) aimDependency() model.ProjectItem {
	item := m.currentItem().ProjectItem
	m.depItemID = item.ID
	m.depItemName = item.Title
	m.depItemHandle = itemHandle(item)
	return item
}

func (m *App) startLinkDependency() tea.Cmd {
	m.aimDependency()
	return fetchDepCandidatesCmd(m.backend, m.allProjects)
}

func (m *App) startUnlinkDependency() tea.Cmd {
	return fetchDepBlockersCmd(m.backend, m.aimDependency().ID)
}

func (m *App) currentTask() model.ProjectItemTask {
	row := m.currentRow()
	return m.itemTasks[m.items[row.itemIdx].ID][row.taskIdx]
}

func (m *App) toggleTaskDone() tea.Cmd {
	return toggleTaskCmd(m.backend, m.currentItem().ID, m.currentTask())
}

func (m *App) deleteTask() tea.Cmd {
	task := m.currentTask()
	return deleteTaskCmd(m.backend, m.currentItem().ID, task.ID)
}

// startAddTask adds a task to the item on the cursor's row, or to a task's item.
func (m *App) startAddTask() tea.Cmd {
	item := m.currentItem()
	m.addTaskItemID = item.ID
	m.addTaskItemTitle = item.Title
	return m.prompt(modeAddTask, "", "New task...")
}

func (m *App) mapOnItem() tea.Cmd {
	return m.switchView(viewDependencies, m.currentItem().ID)
}

// --- Moving a row ---

func (m *App) isMovingItem(id string) bool {
	return m.moveItemPos < len(m.items) && m.items[m.moveItemPos].ID == id
}

func (m *App) grabItem() tea.Cmd {
	m.moveItemPos = m.currentRow().itemIdx
	m.appMode = modeMove
	return nil
}

func (m *App) grabProject() tea.Cmd {
	m.moveProjectPos = m.projectCursor
	m.appMode = modeMoveProject
	return nil
}

// moveGrabbed swaps the grabbed row with its neighbor, and the cursor follows it.
func (m *App) moveGrabbed(direction int) {
	if m.appMode == modeMoveProject {
		to := m.moveProjectPos + direction
		if to < 0 || to >= len(m.projects) {
			return
		}
		m.projects[m.moveProjectPos], m.projects[to] = m.projects[to], m.projects[m.moveProjectPos]
		m.moveProjectPos, m.projectCursor = to, to
		return
	}
	to := m.moveItemPos + direction
	if to < 0 || to >= len(m.items) {
		return
	}
	m.items[m.moveItemPos], m.items[to] = m.items[to], m.items[m.moveItemPos]
	m.moveItemPos = to
	m.rebuildRows("")
	m.rowCursor = m.firstRowForItem(m.moveItemPos)
	m.scrolled = false
}

func (m *App) moveGrabbedToEnd(direction int) {
	limit := len(m.items)
	if m.appMode == modeMoveProject {
		limit = len(m.projects)
	}
	for range limit {
		m.moveGrabbed(direction)
	}
}

func (m *App) dropGrabbed() tea.Cmd {
	if m.appMode == modeMoveProject {
		m.appMode = modeNormal
		return reorderProjectCmd(m.backend, m.projects[m.moveProjectPos].ID, m.moveProjectPos)
	}
	m.appMode = modeNormal
	item := m.items[m.moveItemPos]
	var above *model.ProjectItemInProject
	if m.moveItemPos > 0 {
		above = &m.items[m.moveItemPos-1]
	}
	return reorderItemCmd(m.backend, item.ID, m.projects[m.projectCursor].ID, above)
}

// cancelGrab reads the list again, which puts the grabbed row back where it was.
func (m *App) cancelGrab() tea.Cmd {
	if m.appMode == modeMoveProject {
		m.appMode = modeNormal
		return fetchProjectsCmd(m.backend, m.projectStatusFilter())
	}
	m.appMode = modeNormal
	return m.fetchItems()
}

// --- Dependency link overlay ---

// updateDepFilter hands typing or a paste to the filter and narrows the list to it.
func (m *App) updateDepFilter(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.depFilter, cmd = m.depFilter.Update(msg)
	query := m.depFilter.Value()
	if query == "" {
		m.depItems = flattenDepGroups(m.depAllItems)
	} else {
		m.depItems = flattenDepGroups(filterDepGroups(m.depAllItems, query))
	}
	m.depCursor = 0
	return cmd
}

func (m *App) startDepFilter() tea.Cmd {
	m.depFiltering = true
	return m.depFilter.Focus()
}

// stopDepFilter hands the keys back to the list, keeping what the filter kept or clearing it.
func (m *App) stopDepFilter(clear bool) tea.Cmd {
	if clear && m.depFilter.Value() != "" {
		m.depFilter.SetValue("")
		m.depItems = flattenDepGroups(m.depAllItems)
		m.depCursor = 0
	}
	m.depFilter.Blur()
	m.depFiltering = false
	return nil
}

func (m *App) moveDepCursor(to int) {
	m.depCursor = max(0, min(to, len(m.depItems)-1))
}

// chooseDependency links the item under the cursor as one this item waits on, or unlinks it.
func (m *App) chooseDependency() tea.Cmd {
	selected := m.depItems[m.depCursor]
	unlinking := m.appMode == modeDepUnlink
	m.appMode = modeNormal
	if unlinking {
		return removeDependencyCmd(m.backend, m.depItemID, selected.ID)
	}
	return addDependencyCmd(m.backend, m.depItemID, selected.ID)
}

func (m *App) cancelDepLink() tea.Cmd {
	m.appMode = modeNormal
	return nil
}

// syncScroll adjusts scroll offset so the cursor stays visible within the viewport.
func syncScroll(cursor, scroll, viewHeight int) int {
	if viewHeight <= 0 {
		return 0
	}
	if cursor < scroll {
		return cursor
	}
	if cursor >= scroll+viewHeight {
		return cursor - viewHeight + 1
	}
	return scroll
}

// --- Drawing ---

// Bubble Tea draws a view from its top and loses the rows past the window, so View
// cuts a taller screen to its last rows, where an overlay's hints are.
func (m *App) View() tea.View {
	v := tea.NewView(lastRows(m.screen(), m.height))
	v.AltScreen = true
	return v
}

func lastRows(screen string, height int) string {
	rows := strings.Split(screen, "\n")
	if height <= 0 || len(rows) <= height {
		return screen
	}
	return strings.Join(rows[len(rows)-height:], "\n")
}

func (m *App) screen() string {
	if m.width == 0 {
		return "Loading..."
	}
	if m.reader != nil {
		return m.reader.View().Content
	}

	switch m.appMode {
	case modeAddItem, modeAddItemMulti, modeAddProject, modeAddTask, modeRenameItem, modeRenameProject, modeDropProject:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.renderInputOverlay())
	case modeProjectPicker:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.picker.view(m.width, m.footerHints()))
	case modeHelp:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.renderHelpOverlay())
	case modeDepLink, modeDepUnlink:
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.renderDepLinkOverlay())
	}

	header := m.renderViewHeader()
	statusBar := m.renderStatusBar()
	bodyHeight := max(m.height-lipgloss.Height(header)-lipgloss.Height(statusBar), 5)
	paneHeight := bodyHeight - 2

	var body string
	switch m.view {
	case viewDependencies:
		body = m.renderDepTreeView(paneHeight)
	case viewItems:
		body = paneBox(true, m.width, paneHeight).Render(m.renderItemPane(m.itemPaneWidth()-2, paneHeight))
	default:
		projectPaneWidth := m.projectPaneWidth()
		left := paneBox(m.activePane == projectPane, projectPaneWidth, paneHeight).Render(m.renderProjectPane(projectPaneWidth-2, paneHeight))
		right := paneBox(m.activePane != projectPane, m.itemPaneWidth(), paneHeight).Render(m.renderItemPane(m.itemPaneWidth()-2, paneHeight))
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, statusBar)
}

// itemPaneWidth counts the pane's border.
func (m *App) itemPaneWidth() int {
	if m.view == viewItems {
		return m.width
	}
	return max(m.width-m.projectPaneWidth(), 20)
}

// renderViewHeader names every view, the one showing bold in the active border's color, so
// Tab's effect is read off the top line.
func (m *App) renderViewHeader() string {
	parts := make([]string, len(views))
	for i, v := range views {
		if view(i) == m.view {
			parts[i] = activeViewStyle.Render(v.label)
		} else {
			parts[i] = dimStyle.Render(v.label)
		}
	}
	return truncate(" ", m.width) + strings.Join(parts, dimStyle.Render("   "))
}

func (m *App) renderInputOverlay() string {
	var prompt string
	switch m.appMode {
	case modeAddItem:
		prompt = "New item"
	case modeAddItemMulti:
		prompt = "New item (multi-project)"
	case modeAddProject:
		prompt = "New project"
	case modeAddTask:
		prompt = fmt.Sprintf("New task for %q", truncate(m.addTaskItemTitle, 40))
	case modeRenameItem:
		prompt = "Rename the item"
	case modeRenameProject:
		prompt = "Rename the project"
	case modeDropProject:
		prompt = fmt.Sprintf("Drop %q — why, rather than deferring it?", truncate(m.promptProject.Name, 40))
	}
	lines := []string{
		overlayTitleStyle.Render(prompt),
		"",
		"  " + inputView(m.titleInput),
		"",
		dimStyle.Render("  " + m.footerHints()),
	}
	return overlayBox(strings.Join(lines, "\n"), m.width, 30, 60)
}

// renderHelpOverlay is the action table under the overlay, as ? lists it.
func (m *App) renderHelpOverlay() string {
	const narrowest, widest = 50, 70
	shown := m.appMode
	m.appMode = m.returnMode
	keys := m.helpLines(overlayContentWidth(m.width, narrowest, widest))
	m.appMode = shown

	lines := []string{overlayTitleStyle.Render("Keys"), ""}
	lines = append(lines, keys...)
	lines = append(lines, "", dimStyle.Render("  "+m.footerHints()))
	return overlayBox(strings.Join(lines, "\n"), m.width, narrowest, widest)
}

func (m *App) renderDepLinkOverlay() string {
	var header string
	if m.appMode == modeDepUnlink {
		header = fmt.Sprintf("Unlink dependency from: %s (%s)", m.depItemName, m.depItemHandle)
	} else {
		header = fmt.Sprintf("Link dependency for: %s (%s)", m.depItemName, m.depItemHandle)
	}

	lines := []string{overlayTitleStyle.Render(header), ""}
	// Unlink lists the few items this one waits on, which need no filter.
	if m.appMode == modeDepLink {
		lines = append(lines, "  "+inputView(m.depFilter), "")
	}
	if len(m.depItems) == 0 {
		if m.depFilter.Value() != "" {
			lines = append(lines, dimStyle.Render("  No matching items"))
		} else {
			lines = append(lines, dimStyle.Render("  No items available"))
		}
	}

	visibleSet := make(map[string]bool, len(m.depItems))
	for _, item := range m.depItems {
		visibleSet[item.ID] = true
	}
	maxVisible := max(m.height-12, 5)
	scrollStart := 0
	if m.depCursor > maxVisible-3 {
		scrollStart = m.depCursor - maxVisible + 3
	}
	line := func(item model.ProjectItem, at int, indent string) string {
		status := "○"
		if item.Completed {
			status = "✓"
		}
		text := fmt.Sprintf("%s %s  %s", status, item.Title, itemIDStyle.Render(itemHandle(item)))
		if at == m.depCursor {
			return pickerSelectedStyle.Render("> " + text)
		}
		return pickerNormalStyle.Render(indent + text)
	}

	if m.appMode == modeDepLink {
		flatIdx := 0
		for _, g := range m.depAllItems {
			var groupItems []model.ProjectItem
			for _, item := range g.items {
				if visibleSet[item.ID] {
					groupItems = append(groupItems, item)
				}
			}
			if len(groupItems) == 0 {
				continue
			}
			lines = append(lines, dimStyle.Render("  ── "+g.projectName+" ──"))
			for _, item := range groupItems {
				if flatIdx >= scrollStart && flatIdx < scrollStart+maxVisible {
					lines = append(lines, line(item, flatIdx, "    "))
				}
				flatIdx++
			}
		}
	} else {
		for i, item := range m.depItems {
			if i >= scrollStart && i < scrollStart+maxVisible {
				lines = append(lines, line(item, i, "  "))
			}
		}
	}

	lines = append(lines, "", dimStyle.Render("  "+m.footerHints()))
	return overlayBox(strings.Join(lines, "\n"), m.width, 40, 80)
}

// clipHints narrows a hint line to fit, dropping whole hints rather than
// cutting one in half: a trailing "T re…" reads as a rendering fault, where a
// dropped hint reads as a list that continues. Hints are split on the gap
// between them, since each carries a space of its own.
func clipHints(hints string, maxWidth int) string {
	if lipgloss.Width(hints) <= maxWidth {
		return hints
	}
	if maxWidth < 4 {
		return ""
	}
	parts := strings.Split(hints, hintGap)
	kept, width := []string{}, 0
	for i, part := range parts {
		next := width + lipgloss.Width(part)
		if i > 0 {
			next += len(hintGap)
		}
		if next+2 > maxWidth {
			break
		}
		width, kept = next, append(kept, part)
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, hintGap) + " …"
}

func (m *App) statusBarHints() string {
	switch {
	case m.statusMsg != "":
		return statusMsgStyle.Render(m.statusMsg)
	case m.appMode == modeMove || m.appMode == modeMoveProject:
		return moveIndicatorStyle.Render(m.footerHints())
	}
	return m.footerHints()
}

func (m *App) renderStatusBar() string {
	var modeStr string
	switch {
	case m.where.APIHost != "":
		modeStr = modeAPIStyle.Render("API") + dimStyle.Render(" "+m.where.APIHost)
	default:
		modeStr = modeLocalStyle.Render("LOCAL") + dimStyle.Render(" "+m.where.DatabasePath)
	}

	// ? sits on the right, with the things that are always true, rather than at the end of the
	// contextual list where clipping reaches it first. It is the one key that leads to every
	// other key, so it is the last one that should disappear on a narrow window.
	modeStr = "? keys  " + modeStr
	modeWidth := lipgloss.Width(modeStr)

	var left string
	switch {
	case m.errorMsg != "":
		left = errorMsgStyle.Render("Error: " + m.errorMsg)
	case m.loading:
		left = dimStyle.Render("Loading...")
	case m.statusMsg != "":
		left = statusMsgStyle.Render(m.statusMsg)
	default:
		// Clipped rather than wrapped, and cut before styling so no escape sequence is cut in
		// half. The 5 is the two spaces this function adds, the style's own left and right
		// padding, and the one column of gap that always separates the hints from the mode.
		left = clipHints(m.statusBarHints(), m.width-modeWidth-5)
	}

	padding := max(m.width-lipgloss.Width(left)-modeWidth-4, 1)
	bar := fmt.Sprintf(" %s%s%s ", left, strings.Repeat(" ", padding), modeStr)
	return statusBarStyle.Render(bar)
}
