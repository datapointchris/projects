package tui

// view is one tab of the board. Tab cycles them in this order, and the header names the
// one showing.
type view int

const (
	viewProjects     view = iota // the projects beside the items of the one under the cursor
	viewItems                    // every item, grouped by the project it is in
	viewDependencies             // the dependency map beside the tree the cursor stands in
)

// views is what --view takes for each view, and what the header calls it.
var views = []struct{ name, label string }{
	viewProjects:     {"projects", "By project"},
	viewItems:        {"items", "All items"},
	viewDependencies: {"dependencies", "Dependencies"},
}

// ViewNames is every view --view opens on, in the order Tab cycles them.
func ViewNames() []string {
	names := make([]string, len(views))
	for i, v := range views {
		names[i] = v.name
	}
	return names
}

type pane int

const (
	projectPane pane = iota
	itemPane
)

type appMode int

const (
	modeNormal        appMode = iota
	modeAddItem               // text input: new item in current project
	modeAddItemMulti          // text input → project picker: multi-project add
	modeAddProject            // text input: new project
	modeAddTask               // text input: new sub-task on the item pane's item
	modeRenameItem            // text input: the item's new title
	modeProjectPicker         // checkbox picker: manage project membership
	modeExternalEdit          // $EDITOR holds the terminal: notes or a project description
	modeHelp                  // the action table as an overlay
	modeFilter                // the filter is typed, and the list narrows under each key
	modeMove                  // grab item, j/k to reposition
	modeMoveProject           // grab project, j/k to reposition
	modeDepLink               // pick item to add as dependency
	modeDepUnlink             // pick existing dependency to remove
	modeRenameProject         // text input: the project's new name
	modeDropProject           // text input: the reason a project is dropped rather than deferred
)

type pickerIntent int

const (
	pickerManage pickerIntent = iota // manage existing item's project membership
	pickerCreate                     // select projects for new item
)

type filterMode int

const (
	filterNone    filterMode = iota
	filterBlocked            // show only blocked items
	filterAll                // include archived items
)
