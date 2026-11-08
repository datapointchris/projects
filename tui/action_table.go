package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/datapointchris/projects/model"
)

// action is one row of the key table. The key handler, the footer and the ? overlay all read
// the same rows, so a key bound in one shows in the other two.
type action struct {
	keys []string
	// section heads the action's group in the ? overlay.
	section string
	label   string
	// hint is the footer's word for it, such as "[a]dd", or empty to leave it to the overlay.
	hint    string
	run     func(*App) tea.Cmd
	applies func(*App) bool
}

const (
	sectionMove     = "Moving"
	sectionProject  = "On a project"
	sectionItem     = "On an item"
	sectionTask     = "On a task"
	sectionGroup    = "On a project's group"
	sectionViews    = "Views and filters"
	sectionMap      = "In the dependency map"
	sectionMoving   = "Moving a row"
	sectionEveryone = "Everywhere"
)

// actions is every key the screen can use now, so a key it cannot use is in none of the
// handler, the footer and the overlay.
func (m *App) actions() []action {
	var table []action
	switch {
	case m.appMode == modeMove || m.appMode == modeMoveProject:
		table = moveActions()
	case m.view == viewDependencies:
		table = slices.Concat(mapActions(), viewActions(), everywhereActions())
	case m.view == viewProjects && m.activePane == projectPane:
		table = slices.Concat(listMoveActions(), projectActions(), viewActions(), everywhereActions())
	default:
		table = slices.Concat(listMoveActions(), itemActions(), taskActions(), groupActions(), viewActions(), everywhereActions())
	}
	return slices.DeleteFunc(table, func(a action) bool { return a.applies != nil && !a.applies(m) })
}

// runKey runs the action bound to key, and reports whether one was.
func (m *App) runKey(key string) (tea.Cmd, bool) {
	for _, a := range m.actions() {
		if slices.Contains(a.keys, key) {
			return a.run(m), true
		}
	}
	return nil, false
}

func (m *App) footerHints() string {
	var hints []string
	for _, a := range m.actions() {
		if a.hint != "" {
			hints = append(hints, a.hint)
		}
	}
	return strings.Join(hints, " ")
}

// keyNames is how the overlay writes a key Bubble Tea names otherwise.
var keyNames = map[string]string{
	"down": "↓", "up": "↑", "left": "←", "right": "→",
	"enter": "Enter", "esc": "Esc", "space": "space", "tab": "Tab", "shift+tab": "Shift+Tab",
	"home": "Home", "end": "End", "pgdown": "PgDn", "pgup": "PgUp",
	"ctrl+d": "Ctrl+d", "ctrl+u": "Ctrl+u", "ctrl+c": "Ctrl+c",
}

func keysText(keys []string) string {
	named := make([]string, len(keys))
	for i, k := range keys {
		if n, ok := keyNames[k]; ok {
			k = n
		}
		named[i] = k
	}
	return strings.Join(named, " ")
}

// helpLines is the ? overlay: every action the screen can use now, under its section.
func (m *App) helpLines() []string {
	var lines []string
	section := ""
	for _, a := range m.actions() {
		if a.section != section {
			if section != "" {
				lines = append(lines, "")
			}
			section = a.section
			lines = append(lines, "  "+overlayTitleStyle.Render(section))
		}
		lines = append(lines, "  "+padRight(keysText(a.keys), 18)+a.label)
	}
	return lines
}

func padRight(s string, width int) string {
	if n := len([]rune(s)); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s + " "
}

// --- The tables ---

func listMoveActions() []action {
	return []action{
		{keys: []string{"j", "down"}, section: sectionMove, label: "move down a row", run: func(m *App) tea.Cmd { return m.cursorDown() }},
		{keys: []string{"k", "up"}, section: sectionMove, label: "move up a row", run: func(m *App) tea.Cmd { return m.cursorUp() }},
		{keys: []string{"g", "home"}, section: sectionMove, label: "go to the top", run: func(m *App) tea.Cmd { return m.cursorTop() }},
		{keys: []string{"G", "end"}, section: sectionMove, label: "go to the bottom", run: func(m *App) tea.Cmd { return m.cursorBottom() }},
		{
			keys: []string{"ctrl+d", "pgdown"}, section: sectionMove, label: "half a screen down the open note, or down the list",
			run: func(m *App) tea.Cmd { return m.halfScreen(1) },
		},
		{
			keys: []string{"ctrl+u", "pgup"}, section: sectionMove, label: "half a screen up the open note, or up the list",
			run: func(m *App) tea.Cmd { return m.halfScreen(-1) },
		},
		{
			keys: []string{"]"}, section: sectionMove, label: "jump to the next item or group, skipping tasks",
			run: func(m *App) tea.Cmd { m.jumpRow(1); return nil }, applies: (*App).onItemList,
		},
		{
			keys: []string{"["}, section: sectionMove, label: "jump to the previous item or group, skipping tasks",
			run: func(m *App) tea.Cmd { m.jumpRow(-1); return nil }, applies: (*App).onItemList,
		},
		{
			keys: []string{"J"}, section: sectionMove, label: "scroll the open note down a line", hint: "[J/K]scroll",
			run: func(m *App) tea.Cmd { m.scrollOpenText(1); return nil }, applies: (*App).onOpenText,
		},
		{
			keys: []string{"K"}, section: sectionMove, label: "scroll the open note up a line",
			run: func(m *App) tea.Cmd { m.scrollOpenText(-1); return nil }, applies: (*App).onOpenText,
		},
		{
			keys: []string{"h", "left"}, section: sectionMove, label: "go to the projects",
			run:     func(m *App) tea.Cmd { m.activePane = projectPane; return nil },
			applies: func(m *App) bool { return m.view == viewProjects && m.activePane == itemPane },
		},
		{
			keys: []string{"l", "right"}, section: sectionMove, label: "go to the items",
			run:     func(m *App) tea.Cmd { m.activePane = itemPane; return nil },
			applies: func(m *App) bool { return m.view == viewProjects && m.activePane == projectPane },
		},
	}
}

func projectActions() []action {
	onProject := (*App).onProject
	active := func(m *App) bool { return m.onProject() && m.projects[m.projectCursor].Status == model.StatusActive }
	closed := func(m *App) bool { return m.onProject() && m.projects[m.projectCursor].Status != model.StatusActive }
	return []action{
		{keys: []string{"enter"}, section: sectionProject, label: "read the project", hint: "[Enter]read", run: (*App).readProject, applies: onProject},
		{keys: []string{"space"}, section: sectionProject, label: "mark the project, to list several projects' items", hint: "[space]mark", run: (*App).toggleProjectMark, applies: onProject},
		{keys: []string{"a"}, section: sectionProject, label: "add a project", hint: "[a]dd", run: (*App).startAddProject},
		{keys: []string{"A"}, section: sectionProject, label: "add an item to several projects", run: (*App).startAddItemToSeveral, applies: onProject},
		{keys: []string{"n"}, section: sectionProject, label: "rename the project", run: (*App).startRenameProject, applies: onProject},
		{keys: []string{"e"}, section: sectionProject, label: "edit the description in $EDITOR", hint: "[e]dit", run: (*App).editProjectDescription, applies: onProject},
		{keys: []string{"c"}, section: sectionProject, label: "complete the project", run: (*App).completeProject, applies: active},
		{keys: []string{"x"}, section: sectionProject, label: "drop the project, saying why", run: (*App).startDropProject, applies: active},
		{keys: []string{"S"}, section: sectionProject, label: "shelve the project for someday", run: (*App).shelveProject, applies: active},
		{keys: []string{"r"}, section: sectionProject, label: "reopen the shelved or closed project", hint: "[r]eopen", run: (*App).reopenProject, applies: closed},
		{
			keys: []string{"M"}, section: sectionProject, label: "move the project, then j and k, Enter to drop", hint: "[M]ove",
			run: (*App).grabProject, applies: func(m *App) bool { return m.onProject() && len(m.projects) > 1 && m.projectQuery == "" },
		},
		{keys: []string{"T"}, section: sectionProject, label: "open the dependency map", run: func(m *App) tea.Cmd { return m.switchView(viewDependencies, "") }},
		{keys: []string{"C"}, section: sectionProject, label: "open a Claude session on the project", run: (*App).claudeOnProject, applies: onProject},
	}
}

func itemActions() []action {
	onItem := func(m *App) bool { return m.onKind(rowItem) }
	onItemOrTask := func(m *App) bool { return m.onKind(rowItem) || m.onKind(rowTask) }
	oneProject := func(m *App) bool { return !m.isGroupedView() && m.projectCursor < len(m.projects) }
	return []action{
		{keys: []string{"enter"}, section: sectionItem, label: "read the item", hint: "[Enter]read", run: (*App).readItem, applies: onItem},
		{
			keys: []string{"space"}, section: sectionItem, label: "open or close the note in place", hint: "[space]note",
			run: (*App).toggleNote, applies: func(m *App) bool { return onItem(m) && hasNote(m.currentItem().ProjectItem) },
		},
		{keys: []string{"c"}, section: sectionItem, label: "mark the item done or not done", hint: "[c]omplete", run: (*App).toggleItemDone, applies: onItem},
		{keys: []string{"a"}, section: sectionItem, label: "add an item to this project", hint: "[a]dd", run: (*App).startAddItem, applies: oneProject},
		{keys: []string{"A"}, section: sectionItem, label: "add an item to several projects", run: (*App).startAddItemToSeveral, applies: func(m *App) bool { return len(m.allProjects) > 0 }},
		{keys: []string{"e"}, section: sectionItem, label: "edit the note in $EDITOR", hint: "[e]dit", run: (*App).editItemNote, applies: onItem},
		{keys: []string{"n"}, section: sectionItem, label: "rename the item", run: (*App).startRenameItem, applies: onItem},
		{keys: []string{"x"}, section: sectionItem, label: "archive the item", run: (*App).archiveItem, applies: onItem},
		{keys: []string{"P"}, section: sectionItem, label: "choose the projects the item is filed in", run: (*App).manageItemProjects, applies: onItem},
		{keys: []string{"b"}, section: sectionItem, label: "link an item this one waits on", run: (*App).startLinkDependency, applies: onItem},
		{keys: []string{"B"}, section: sectionItem, label: "unlink an item this one waits on", run: (*App).startUnlinkDependency, applies: onItem},
		{
			keys: []string{"M"}, section: sectionItem, label: "move the item, then j and k, Enter to drop", hint: "[M]ove",
			run: (*App).grabItem, applies: func(m *App) bool { return onItem(m) && oneProject(m) && len(m.items) > 1 && m.itemQuery == "" },
		},
		{keys: []string{"C"}, section: sectionItem, label: "open a Claude session on the item", run: (*App).claudeOnItem, applies: onItem},
		{keys: []string{"t"}, section: sectionItem, label: "add a task to the item", run: (*App).startAddTask, applies: onItemOrTask},
		{keys: []string{"T"}, section: sectionItem, label: "open the dependency map on the item", run: (*App).mapOnItem, applies: onItemOrTask},
	}
}

func taskActions() []action {
	onTask := func(m *App) bool { return m.onKind(rowTask) }
	return []action{
		{keys: []string{"space", "c"}, section: sectionTask, label: "mark the task done or not done", hint: "[space]done", run: (*App).toggleTaskDone, applies: onTask},
		{keys: []string{"D"}, section: sectionTask, label: "delete the task", hint: "[D]elete", run: (*App).deleteTask, applies: onTask},
	}
}

func groupActions() []action {
	return []action{
		{
			keys: []string{"enter", "space"}, section: sectionGroup, label: "fold or open the project's items", hint: "[Enter]fold",
			run: (*App).toggleGroup, applies: func(m *App) bool { return m.onKind(rowGroup) },
		},
	}
}

func viewActions() []action {
	return []action{
		{keys: []string{"esc"}, section: sectionViews, label: "clear the filter, then the marks, or back out of a drill", hint: "[Esc]back", run: (*App).backOut, applies: (*App).canBackOut},
		{keys: []string{"tab"}, section: sectionViews, label: "the next view", hint: "[Tab]view", run: func(m *App) tea.Cmd { return m.cycleView(1) }},
		{keys: []string{"shift+tab"}, section: sectionViews, label: "the previous view", run: func(m *App) tea.Cmd { return m.cycleView(-1) }},
		{keys: []string{"/"}, section: sectionViews, label: "filter the list, matching notes too", hint: "[/]filter", run: (*App).startFilter},
		{keys: []string{"1"}, section: sectionViews, label: "show only blocked items, or every one again", run: func(m *App) tea.Cmd { return m.setFilter(filterBlocked) }, applies: notMap},
		{keys: []string{"2"}, section: sectionViews, label: "show archived items too, or hide them again", run: func(m *App) tea.Cmd { return m.setFilter(filterAll) }, applies: notMap},
		{
			keys: []string{"0"}, section: sectionViews, label: "show every item but the archived", hint: "[0]reset",
			run: func(m *App) tea.Cmd { return m.setFilter(filterNone) }, applies: func(m *App) bool { return notMap(m) && m.filter != filterNone },
		},
		{keys: []string{"."}, section: sectionViews, label: "show or hide shelved and closed projects", run: (*App).toggleClosedProjects, applies: notMap},
	}
}

func notMap(m *App) bool { return m.view != viewDependencies }

func everywhereActions() []action {
	return []action{
		{keys: []string{"u"}, section: sectionEveryone, label: "undo the last change", run: func(m *App) tea.Cmd { return undoCmd(m.backend) }},
		{keys: []string{"R"}, section: sectionEveryone, label: "read the data again now", run: (*App).readAgain},
		{keys: []string{"?"}, section: sectionEveryone, label: "show these keys", run: func(m *App) tea.Cmd { m.returnMode = m.appMode; m.appMode = modeHelp; return nil }},
		{keys: []string{"q", "ctrl+c"}, section: sectionEveryone, label: "quit", run: func(*App) tea.Cmd { return tea.Quit }},
	}
}

func mapActions() []action {
	focused := func(m *App) bool { return m.depTree.active == focusPane }
	return []action{
		{keys: []string{"j", "down"}, section: sectionMove, label: "move down a row", run: func(m *App) tea.Cmd { m.depTreeMove(1); return nil }},
		{keys: []string{"k", "up"}, section: sectionMove, label: "move up a row", run: func(m *App) tea.Cmd { m.depTreeMove(-1); return nil }},
		{keys: []string{"g", "home"}, section: sectionMove, label: "go to the top", run: func(m *App) tea.Cmd { m.depTreeJump(0); return nil }},
		{keys: []string{"G", "end"}, section: sectionMove, label: "go to the bottom", run: func(m *App) tea.Cmd { m.depTreeJump(-1); return nil }},
		{keys: []string{"ctrl+d", "pgdown"}, section: sectionMove, label: "half a screen down", run: func(m *App) tea.Cmd { m.depTreeMove(m.depTreePaneHeight() / 2); return nil }},
		{keys: []string{"ctrl+u", "pgup"}, section: sectionMove, label: "half a screen up", run: func(m *App) tea.Cmd { m.depTreeMove(-m.depTreePaneHeight() / 2); return nil }},
		{keys: []string{"l", "right"}, section: sectionMap, label: "go into the tree, then drill into the row", hint: "[l]drill", run: (*App).depTreeRight},
		{
			keys: []string{"h", "left"}, section: sectionMap, label: "back out a drill, then back to the map", hint: "[h]back",
			run: (*App).depTreeLeft, applies: focused,
		},
		{keys: []string{"enter"}, section: sectionMap, label: "read the item", hint: "[Enter]read", run: (*App).readMapItem, applies: func(m *App) bool { return m.depTree.activeID() != "" }},
		{keys: []string{"i"}, section: sectionMap, label: "read the tree's edges the other way", hint: "[i]flip", run: (*App).depTreeFlip, applies: focused},
		{keys: []string{"."}, section: sectionMap, label: "show or hide the finished trees", hint: "[.]finished", run: (*App).depTreeToggleFinished},
	}
}

func moveActions() []action {
	return []action{
		{keys: []string{"j", "down"}, section: sectionMoving, label: "move it down", hint: "[j/k]move", run: func(m *App) tea.Cmd { m.moveGrabbed(1); return nil }},
		{keys: []string{"k", "up"}, section: sectionMoving, label: "move it up", run: func(m *App) tea.Cmd { m.moveGrabbed(-1); return nil }},
		{keys: []string{"g", "home"}, section: sectionMoving, label: "move it to the top", hint: "[g/G]top/bottom", run: func(m *App) tea.Cmd { m.moveGrabbedToEnd(-1); return nil }},
		{keys: []string{"G", "end"}, section: sectionMoving, label: "move it to the bottom", run: func(m *App) tea.Cmd { m.moveGrabbedToEnd(1); return nil }},
		{keys: []string{"enter"}, section: sectionMoving, label: "drop it here", hint: "[Enter]drop", run: (*App).dropGrabbed},
		{keys: []string{"esc"}, section: sectionMoving, label: "put it back", hint: "[Esc]cancel", run: (*App).cancelGrab},
		{keys: []string{"?"}, section: sectionEveryone, label: "show these keys", run: func(m *App) tea.Cmd { m.returnMode = m.appMode; m.appMode = modeHelp; return nil }},
		{keys: []string{"ctrl+c"}, section: sectionEveryone, label: "quit", run: func(*App) tea.Cmd { return tea.Quit }},
	}
}
