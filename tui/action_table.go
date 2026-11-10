package tui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/datapointchris/gotermboard"

	"github.com/datapointchris/projects/model"
)

// action is one row of the key table, in gotermboard's shape. The key handler, the footer and the
// ? overlay all read the same rows, so a key bound in one shows in the other two. Footer is the
// footer's words for the key, or empty to leave it to the overlay, and Section heads its group
// in the overlay.
type action = gotermboard.KeyAction[*App]

const (
	sectionProject = "On a project"
	sectionItem    = "On an item"
	sectionTask    = "On a task"
	sectionViews   = "Views and filters"
	sectionMap     = "In the dependency map"
	sectionMoving  = "Moving a row"
	sectionPrompt  = "Typing"
	sectionPicker  = "Choosing projects"
	sectionLinking = "Choosing a dependency"
	sectionKeys    = "This list"
)

// actions is every key the screen can use now, so a key it cannot use is in none of the
// handler, the footer and the overlay. On the board, its own rows come ahead of gotermboard's
// universal ones, so a key of its own wins where it applies. A screen with a text box binds only
// keys that type nothing, and the box takes the rest.
func (m *App) actions() []action {
	var table []action
	switch {
	case m.appMode == modeMove || m.appMode == modeMoveProject:
		table = moveActions()
	case m.appMode == modeHelp:
		table = helpActions()
	case m.appMode == modeFilter:
		table = filterActions()
	case m.appMode == modeProjectPicker:
		table = pickerActions()
	case m.appMode == modeDepLink || m.appMode == modeDepUnlink:
		table = depLinkActions()
	case m.appMode == modeExternalEdit:
		return nil
	case m.appMode != modeNormal:
		table = promptActions()
	case m.view == viewDependencies:
		return gotermboard.ActionsNow(m, slices.Concat(mapActions(), viewActions(), everywhereActions()), gotermboard.UniversalActions[*App]())
	case m.view == viewProjects && m.activePane == projectPane:
		return gotermboard.ActionsNow(m, slices.Concat(paneActions(), projectActions(), viewActions(), everywhereActions()), gotermboard.UniversalActions[*App]())
	default:
		return gotermboard.ActionsNow(m, slices.Concat(paneActions(), itemActions(), taskActions(), viewActions(), everywhereActions()), gotermboard.UniversalActions[*App]())
	}
	return gotermboard.ActionsNow(m, table)
}

// runKey runs the action bound to key, and reports whether one was.
func (m *App) runKey(key string) (tea.Cmd, bool) {
	return gotermboard.RunKey(m, m.actions(), key)
}

// footerHints is the footer's words for each key the screen can use now, two spaces apart. The
// status bar pins ? at its right, so the list leaves it out.
func (m *App) footerHints() string {
	shown := slices.DeleteFunc(m.actions(), func(a action) bool { return a.Keys[0] == "?" })
	return strings.Join(gotermboard.FooterHints(shown), hintGap)
}

// hintGap parts one hint from the next, which a hint's own words never hold.
const hintGap = "  "

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

// helpLines is the ? overlay: every action the screen can use now, under its section in the
// order each section first appears, each label wrapped at width under its own column so no word
// of it reads as a key.
func (m *App) helpLines(width int) []string {
	var lines []string
	for i, section := range gotermboard.BySection(m.actions()) {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "  "+overlayTitleStyle.Render(section.Section))
		for _, a := range section.Actions {
			keys := "  " + padRight(keysText(a.Keys), 18)
			indent := strings.Repeat(" ", lipgloss.Width(keys))
			label := strings.Split(ansi.Wordwrap(a.Label, max(width-len(indent), 10), ""), "\n")
			lines = append(lines, keys+label[0])
			for _, rest := range label[1:] {
				lines = append(lines, indent+rest)
			}
		}
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

// paneActions is moving between the panes of the projects view. gotermboard's universal rows move
// within a pane.
func paneActions() []action {
	return []action{
		{
			Keys: []string{"h", "left"}, Section: gotermboard.SectionMoving, Label: "go to the projects",
			Run:     func(m *App) tea.Cmd { m.activePane = projectPane; return nil },
			Applies: func(m *App) bool { return m.view == viewProjects && m.activePane == itemPane },
		},
		{
			Keys: []string{"l", "right"}, Section: gotermboard.SectionMoving, Label: "go to the items",
			Run:     func(m *App) tea.Cmd { m.activePane = itemPane; return nil },
			Applies: func(m *App) bool { return m.view == viewProjects && m.activePane == projectPane },
		},
	}
}

func projectActions() []action {
	onProject := (*App).onProject
	active := func(m *App) bool { return m.onProject() && m.projects[m.projectCursor].Status == model.StatusActive }
	closed := func(m *App) bool { return m.onProject() && m.projects[m.projectCursor].Status != model.StatusActive }
	return []action{
		{Keys: []string{"enter"}, Section: sectionProject, Label: "read the project", Footer: "enter read", Run: (*App).readProject, Applies: onProject},
		{Keys: []string{"space"}, Section: sectionProject, Label: "mark the project, to list several projects' items", Footer: "space mark", Run: (*App).toggleProjectMark, Applies: onProject},
		{Keys: []string{"a"}, Section: sectionProject, Label: "add a project", Footer: "a add", Run: (*App).startAddProject},
		{Keys: []string{"A"}, Section: sectionProject, Label: "add an item to several projects", Run: (*App).startAddItemToSeveral, Applies: onProject},
		{Keys: []string{"r"}, Section: sectionProject, Label: "rename the project", Run: (*App).startRenameProject, Applies: onProject},
		{Keys: []string{"e", "n"}, Section: sectionProject, Label: "write the description in $EDITOR", Footer: "e edit", Run: (*App).editProjectDescription, Applies: onProject},
		{Keys: []string{"c"}, Section: sectionProject, Label: "complete the project", Run: (*App).completeProject, Applies: active},
		{Keys: []string{"c"}, Section: sectionProject, Label: "reopen the shelved or closed project", Footer: "c reopen", Run: (*App).reopenProject, Applies: closed},
		{Keys: []string{"x"}, Section: sectionProject, Label: "drop the project, saying why", Run: (*App).startDropProject, Applies: active},
		{Keys: []string{"S"}, Section: sectionProject, Label: "shelve the project for someday", Run: (*App).shelveProject, Applies: active},
		{
			Keys: []string{"M"}, Section: sectionProject, Label: "move the project, then j and k, Enter to drop", Footer: "M move",
			Run: (*App).grabProject, Applies: func(m *App) bool { return m.onProject() && len(m.projects) > 1 && m.projectQuery == "" },
		},
		{Keys: []string{"T"}, Section: sectionProject, Label: "open the dependency map", Run: func(m *App) tea.Cmd { return m.switchView(viewDependencies, "") }},
		{Keys: []string{"C"}, Section: sectionProject, Label: "open a Claude session on the project", Run: (*App).claudeOnProject, Applies: onProject},
	}
}

// itemActions is an item's own keys. gotermboard's enter and space open its detail.
func itemActions() []action {
	onItem := func(m *App) bool { return m.onKind(rowItem) }
	onItemOrTask := func(m *App) bool { return m.onKind(rowItem) || m.onKind(rowTask) }
	oneProject := func(m *App) bool { return !m.isGroupedView() && m.projectCursor < len(m.projects) }
	return []action{
		{Keys: []string{"c"}, Section: sectionItem, Label: "mark the item done or not done", Footer: "c complete", Run: (*App).toggleItemDone, Applies: onItem},
		{Keys: []string{"a"}, Section: sectionItem, Label: "add an item to this project", Footer: "a add", Run: (*App).startAddItem, Applies: oneProject},
		{Keys: []string{"A"}, Section: sectionItem, Label: "add an item to several projects", Run: (*App).startAddItemToSeveral, Applies: func(m *App) bool { return len(m.allProjects) > 0 }},
		{Keys: []string{"n", "e"}, Section: sectionItem, Label: "write the note in $EDITOR", Footer: "n note", Run: (*App).editItemNote, Applies: onItem},
		{Keys: []string{"r"}, Section: sectionItem, Label: "rename the item", Run: (*App).startRenameItem, Applies: onItem},
		{Keys: []string{"x"}, Section: sectionItem, Label: "archive the item", Run: (*App).archiveItem, Applies: onItem},
		{Keys: []string{"P"}, Section: sectionItem, Label: "choose the projects the item is filed in", Run: (*App).manageItemProjects, Applies: onItem},
		{Keys: []string{"b"}, Section: sectionItem, Label: "link an item this one waits on", Run: (*App).startLinkDependency, Applies: onItem},
		{Keys: []string{"B"}, Section: sectionItem, Label: "unlink an item this one waits on", Run: (*App).startUnlinkDependency, Applies: onItem},
		{
			Keys: []string{"M"}, Section: sectionItem, Label: "move the item, then j and k, Enter to drop", Footer: "M move",
			Run: (*App).grabItem, Applies: func(m *App) bool { return onItem(m) && oneProject(m) && len(m.items) > 1 && m.itemQuery == "" },
		},
		{Keys: []string{"C"}, Section: sectionItem, Label: "open a Claude session on the item", Run: (*App).claudeOnItem, Applies: onItem},
		{Keys: []string{"t"}, Section: sectionItem, Label: "add a task to the item", Run: (*App).startAddTask, Applies: onItemOrTask},
		{Keys: []string{"T"}, Section: sectionItem, Label: "open the dependency map on the item", Run: (*App).mapOnItem, Applies: onItemOrTask},
	}
}

func taskActions() []action {
	onTask := func(m *App) bool { return m.onKind(rowTask) }
	return []action{
		{Keys: []string{"space", "c"}, Section: sectionTask, Label: "mark the task done or not done", Footer: "space done", Run: (*App).toggleTaskDone, Applies: onTask},
		{Keys: []string{"D"}, Section: sectionTask, Label: "delete the task", Footer: "D delete", Run: (*App).deleteTask, Applies: onTask},
	}
}

func viewActions() []action {
	return []action{
		{Keys: []string{"esc"}, Section: sectionViews, Label: "clear the filter, then the marks, or back out of a drill", Footer: "esc back", Run: (*App).backOut, Applies: (*App).canBackOut},
		{Keys: []string{"tab"}, Section: sectionViews, Label: "the next view", Footer: "tab view", Run: func(m *App) tea.Cmd { return m.cycleView(1) }},
		{Keys: []string{"shift+tab"}, Section: sectionViews, Label: "the previous view", Run: func(m *App) tea.Cmd { return m.cycleView(-1) }},
		{Keys: []string{"/"}, Section: sectionViews, Label: "filter the list, matching notes too", Footer: "/ filter", Run: (*App).startFilter},
		{Keys: []string{"1"}, Section: sectionViews, Label: "show only blocked items, or every one again", Run: func(m *App) tea.Cmd { return m.setFilter(filterBlocked) }, Applies: notMap},
		{Keys: []string{"2"}, Section: sectionViews, Label: "show archived items too, or hide them again", Run: func(m *App) tea.Cmd { return m.setFilter(filterAll) }, Applies: notMap},
		{
			Keys: []string{"0"}, Section: sectionViews, Label: "show every item but the archived", Footer: "0 reset",
			Run: func(m *App) tea.Cmd { return m.setFilter(filterNone) }, Applies: func(m *App) bool { return notMap(m) && m.filter != filterNone },
		},
		{Keys: []string{"."}, Section: sectionViews, Label: "show or hide shelved and closed projects", Run: (*App).toggleClosedProjects, Applies: notMap},
	}
}

func notMap(m *App) bool { return m.view != viewDependencies }

func everywhereActions() []action {
	return []action{
		{Keys: []string{"u"}, Section: gotermboard.SectionEverywhere, Label: "undo this machine's last change", Run: func(m *App) tea.Cmd { return undoCmd(m.backend) }},
		{Keys: []string{"R"}, Section: gotermboard.SectionEverywhere, Label: "read the data again now", Run: (*App).readAgain},
	}
}

// mapActions is the dependency map's own keys. gotermboard's universal rows move through it.
func mapActions() []action {
	focused := func(m *App) bool { return m.depTree.active == focusPane }
	return []action{
		{Keys: []string{"l", "right"}, Section: sectionMap, Label: "go into the tree, then drill into the row", Footer: "l drill", Run: (*App).depTreeRight},
		{
			Keys: []string{"h", "left"}, Section: sectionMap, Label: "back out a drill, then back to the map", Footer: "h back",
			Run: (*App).depTreeLeft, Applies: focused,
		},
		{Keys: []string{"enter"}, Section: sectionMap, Label: "read the item", Footer: "enter read", Run: (*App).readMapItem, Applies: func(m *App) bool { return m.depTree.activeID() != "" }},
		{Keys: []string{"i"}, Section: sectionMap, Label: "read the tree's edges the other way", Footer: "i flip", Run: (*App).depTreeFlip, Applies: focused},
		{Keys: []string{"."}, Section: sectionMap, Label: "show or hide the finished trees", Footer: ". finished", Run: (*App).depTreeToggleFinished},
	}
}

func moveActions() []action {
	return []action{
		{Keys: []string{"j", "down"}, Section: sectionMoving, Label: "move it down", Footer: "j/k move", Run: func(m *App) tea.Cmd { m.moveGrabbed(1); return nil }},
		{Keys: []string{"k", "up"}, Section: sectionMoving, Label: "move it up", Run: func(m *App) tea.Cmd { m.moveGrabbed(-1); return nil }},
		{Keys: []string{"g", "home"}, Section: sectionMoving, Label: "move it to the top", Footer: "g/G top/bottom", Run: func(m *App) tea.Cmd { m.moveGrabbedToEnd(-1); return nil }},
		{Keys: []string{"G", "end"}, Section: sectionMoving, Label: "move it to the bottom", Run: func(m *App) tea.Cmd { m.moveGrabbedToEnd(1); return nil }},
		{Keys: []string{"enter"}, Section: sectionMoving, Label: "drop it here", Footer: "enter drop", Run: (*App).dropGrabbed},
		{Keys: []string{"esc"}, Section: sectionMoving, Label: "put it back", Footer: "esc cancel", Run: (*App).cancelGrab},
		{Keys: []string{"?"}, Section: gotermboard.SectionEverywhere, Label: "show these keys", Run: func(m *App) tea.Cmd { m.ShowKeys(); return nil }},
		{Keys: []string{"ctrl+c"}, Section: gotermboard.SectionEverywhere, Label: "quit", Run: func(*App) tea.Cmd { return tea.Quit }},
	}
}

func helpActions() []action {
	return []action{
		{Keys: []string{"esc", "?"}, Section: sectionKeys, Label: "close this list", Footer: "esc close", Run: (*App).closeHelp},
		{Keys: []string{"q", "ctrl+c"}, Section: sectionKeys, Label: "quit", Footer: "q quit", Run: func(*App) tea.Cmd { return tea.Quit }},
	}
}

func filterActions() []action {
	return []action{
		{Keys: []string{"enter"}, Section: sectionViews, Label: "keep the filter and go back to the list", Footer: "enter keep", Run: (*App).keepFilter},
		{Keys: []string{"esc"}, Section: sectionViews, Label: "clear the filter", Footer: "esc clear", Run: (*App).clearFilter},
	}
}

// promptActions is a prompt for a name, a title or a reason.
func promptActions() []action {
	return []action{
		{Keys: []string{"enter"}, Section: sectionPrompt, Label: "do it with what is typed", Footer: "enter confirm", Run: (*App).confirmPrompt},
		{Keys: []string{"esc"}, Section: sectionPrompt, Label: "close the prompt, keeping nothing", Footer: "esc cancel", Run: (*App).cancelPrompt},
	}
}

func pickerActions() []action {
	return []action{
		{Keys: []string{"j", "down"}, Section: sectionPicker, Label: "move down a project", Run: func(m *App) tea.Cmd { m.picker.down(); return nil }},
		{Keys: []string{"k", "up"}, Section: sectionPicker, Label: "move up a project", Run: func(m *App) tea.Cmd { m.picker.up(); return nil }},
		{Keys: []string{"g", "home"}, Section: sectionPicker, Label: "go to the top", Run: func(m *App) tea.Cmd { m.picker.cursor = 0; return nil }},
		{
			Keys: []string{"G", "end"}, Section: sectionPicker, Label: "go to the bottom",
			Run: func(m *App) tea.Cmd { m.picker.cursor = max(len(m.picker.projects)-1, 0); return nil },
		},
		{Keys: []string{"space"}, Section: sectionPicker, Label: "tick or untick the project", Footer: "space toggle", Run: func(m *App) tea.Cmd { m.picker.toggle(); return nil }},
		{Keys: []string{"enter"}, Section: sectionPicker, Label: "file the item in the ticked projects", Footer: "enter confirm", Run: (*App).confirmPicker},
		{Keys: []string{"esc"}, Section: sectionPicker, Label: "close, changing nothing", Footer: "esc cancel", Run: (*App).cancelPicker},
	}
}

// depLinkActions is choosing an item this one waits on, or one to unlink. Linking can filter the
// items, and while the filter is typed it takes every key but Enter and Esc.
func depLinkActions() []action {
	typing := func(m *App) bool { return m.depFiltering }
	listing := func(m *App) bool { return !m.depFiltering }
	choosing := func(mode appMode) func(*App) bool {
		return func(m *App) bool { return listing(m) && m.appMode == mode && len(m.depItems) > 0 }
	}
	return []action{
		{
			Keys: []string{"enter"}, Section: sectionLinking, Label: "keep the filter and choose from what it kept", Footer: "enter keep",
			Run: func(m *App) tea.Cmd { return m.stopDepFilter(false) }, Applies: typing,
		},
		{Keys: []string{"esc"}, Section: sectionLinking, Label: "clear the filter", Footer: "esc clear", Run: func(m *App) tea.Cmd { return m.stopDepFilter(true) }, Applies: typing},
		{Keys: []string{"j", "down"}, Section: sectionLinking, Label: "move down an item", Run: func(m *App) tea.Cmd { m.moveDepCursor(m.depCursor + 1); return nil }, Applies: listing},
		{Keys: []string{"k", "up"}, Section: sectionLinking, Label: "move up an item", Run: func(m *App) tea.Cmd { m.moveDepCursor(m.depCursor - 1); return nil }, Applies: listing},
		{Keys: []string{"g", "home"}, Section: sectionLinking, Label: "go to the top", Run: func(m *App) tea.Cmd { m.moveDepCursor(0); return nil }, Applies: listing},
		{Keys: []string{"G", "end"}, Section: sectionLinking, Label: "go to the bottom", Run: func(m *App) tea.Cmd { m.moveDepCursor(len(m.depItems)); return nil }, Applies: listing},
		{Keys: []string{"enter"}, Section: sectionLinking, Label: "link the item this one waits on", Footer: "enter link", Run: (*App).chooseDependency, Applies: choosing(modeDepLink)},
		{Keys: []string{"enter"}, Section: sectionLinking, Label: "unlink the item this one waits on", Footer: "enter unlink", Run: (*App).chooseDependency, Applies: choosing(modeDepUnlink)},
		{
			Keys: []string{"/"}, Section: sectionLinking, Label: "filter the items", Footer: "/ filter", Run: (*App).startDepFilter,
			Applies: func(m *App) bool { return listing(m) && m.appMode == modeDepLink },
		},
		{Keys: []string{"esc"}, Section: sectionLinking, Label: "close, changing nothing", Footer: "esc cancel", Run: (*App).cancelDepLink, Applies: listing},
	}
}
