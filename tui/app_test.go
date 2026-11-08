package tui

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/service"
	"github.com/datapointchris/projects/store/sqlite"
)

// newTestApp returns an app already showing one project's items, sized to
// width x height. Commands are drained synchronously so the model reaches
// the same state the event loop would put it in.
func newTestApp(t *testing.T, width, height int, itemTitles ...string) *App {
	t.Helper()
	inputs := make([]model.CreateProjectItem, len(itemTitles))
	for i, title := range itemTitles {
		inputs[i] = model.CreateProjectItem{Title: title}
	}
	return newTestAppWithItems(t, width, height, inputs)
}

// newTestAppWithItems is newTestApp for the cases that need more than a title,
// such as an item holding a note.
func newTestAppWithItems(t *testing.T, width, height int, inputs []model.CreateProjectItem) *App {
	t.Helper()

	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	b := service.New(sqlite.New(database))
	project, err := b.CreateProject(model.CreateProject{Name: "work"})
	if err != nil {
		t.Fatalf("creating project: %v", err)
	}
	for _, input := range inputs {
		input.ProjectIDs = []string{project.ID}
		if _, err := b.CreateItem(input); err != nil {
			t.Fatalf("creating item %q: %v", input.Title, err)
		}
	}

	app := NewApp(b, Where{DatabasePath: ":memory:"})
	send(t, app, tea.WindowSizeMsg{Width: width, Height: height})
	send(t, app, app.Init()())
	send(t, app, app.fetchItems()())
	app.activePane = itemPane
	return app
}

// send delivers msg to the app and follows the resulting command chain to
// exhaustion, so a create that triggers a refresh has landed before the
// next assertion. Mirrors what the event loop does.
func send(t *testing.T, app *App, msg tea.Msg) {
	t.Helper()
	if msg == nil {
		return
	}
	_, cmd := app.Update(msg)
	drain(t, app, cmd, 16)
}

func drain(t *testing.T, app *App, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || depth == 0 {
		return
	}
	msg := runCmd(cmd)
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			drain(t, app, c, depth-1)
		}
		return
	}
	_, next := app.Update(msg)
	drain(t, app, next, depth-1)
}

// runCmd abandons a command that does not produce a message promptly —
// flash and reread commands are tea.Tick timers that would otherwise
// block the test for their full duration.
func runCmd(cmd tea.Cmd) tea.Msg {
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case msg := <-result:
		return msg
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

func runeKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func pressKey(t *testing.T, app *App, key string) {
	t.Helper()
	send(t, app, runeKey([]rune(key)[0]))
}

// Through an API, an item another machine wrote reaches the board only when it
// reads the API again, which R does at once.
func TestRInAPIModeShowsAnItemAnotherMachineWrote(t *testing.T) {
	app := newTestApp(t, 80, 24, "here at launch")
	app.where = Where{APIHost: "projects.example.com"}
	projects, err := app.backend.ListProjectsByStatus(model.StatusActive)
	if err != nil || len(projects) == 0 {
		t.Fatalf("listing projects: %v", err)
	}
	if _, err := app.backend.CreateItem(model.CreateProjectItem{Title: "written elsewhere", ProjectIDs: []string{projects[0].ID}}); err != nil {
		t.Fatal(err)
	}
	titles := func() []string {
		var held []string
		for _, item := range app.items {
			held = append(held, item.Title)
		}
		return held
	}
	if slices.Contains(titles(), "written elsewhere") {
		t.Fatal("the board showed the item before reading the API again")
	}
	pressKey(t, app, "R")
	if !slices.Contains(titles(), "written elsewhere") {
		t.Errorf("after R the board holds %q, want the item written elsewhere", titles())
	}
}

// A position counts every member of a project and this list hides the archived
// ones, so a drop has to count them or it lands that many rows too high.
func TestAMovedItemLandsWhereItIsDroppedBelowAnArchivedOne(t *testing.T) {
	app := newTestApp(t, 80, 24, "archived", "bravo", "charlie", "delta")
	pressKey(t, app, "x")

	visibleTitles := func() []string {
		titles := make([]string, len(app.items))
		for i, item := range app.items {
			titles[i] = item.Title
		}
		return titles
	}
	move := func(keys string, want ...string) {
		t.Helper()
		pressKey(t, app, "m")
		for _, key := range keys {
			pressKey(t, app, string(key))
		}
		send(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
		if got := visibleTitles(); !slices.Equal(got, want) {
			t.Errorf("after moving with %q the list reads %v, want %v", keys, got, want)
		}
	}

	move("j", "charlie", "bravo", "delta")
	move("k", "bravo", "charlie", "delta")
	move("G", "charlie", "delta", "bravo")
	move("g", "bravo", "charlie", "delta")
}

// A database written by the single-row reorder holds ties, and a tied row can sit
// below the drop while sharing a position with the row above it.
func TestADropBelowATiedRowLandsUnderIt(t *testing.T) {
	member := func(id string, position int) model.ProjectItemInProject {
		return model.ProjectItemInProject{ProjectItem: model.ProjectItem{ID: id}, Position: position}
	}
	members := []model.ProjectItemInProject{member("a", 0), member("b", 1), member("c", 1), member("d", 2)}

	if got := positionBelow(members, "d", "b"); got != 2 {
		t.Errorf("dropping d under b landed at %d, want 2", got)
	}
}

// The add-task prompt is an overlay, so it shows in a window too short for the
// list. An input that is not drawn still swallows every keystroke.
func TestAddTaskPromptVisibleInShortWindow(t *testing.T) {
	app := newTestApp(t, 80, 6, "first", "second", "third")

	pressKey(t, app, "t")

	if app.appMode != modeAddTask {
		t.Fatalf("expected modeAddTask after 't', got %v", app.appMode)
	}
	if !strings.Contains(app.screen(), "New task for") {
		t.Errorf("add-task prompt not rendered in a 6-line window:\n%s", app.screen())
	}
}

func TestAddTaskPromptNamesTargetItem(t *testing.T) {
	app := newTestApp(t, 80, 24, "ship the thing")

	pressKey(t, app, "t")

	if got := app.addTaskItemTitle; got != "ship the thing" {
		t.Errorf("expected target title %q, got %q", "ship the thing", got)
	}
	if view := app.screen(); !strings.Contains(view, "ship the thing") {
		t.Errorf("prompt does not name the target item:\n%s", view)
	}
}

// Escaping must return to normal navigation rather than leaving keystrokes
// captured by an input the user cannot see.
func TestAddTaskEscapeRestoresNavigation(t *testing.T) {
	app := newTestApp(t, 80, 24, "first", "second")

	pressKey(t, app, "t")
	send(t, app, tea.KeyPressMsg{Code: tea.KeyEscape})

	if app.appMode != modeNormal {
		t.Fatalf("expected modeNormal after esc, got %v", app.appMode)
	}

	before := app.rowCursor
	pressKey(t, app, "j")
	if app.rowCursor == before {
		t.Errorf("cursor did not move after esc; keys still captured by the input")
	}
}

func TestAddTaskCreatesTaskOnTargetItem(t *testing.T) {
	app := newTestApp(t, 80, 24, "first")

	pressKey(t, app, "t")
	for _, r := range "write tests" {
		send(t, app, runeKey(r))
	}
	send(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})

	if app.appMode != modeNormal {
		t.Fatalf("expected modeNormal after enter, got %v", app.appMode)
	}

	item := app.items[0]
	tasks, err := app.backend.ListTasks(item.ID)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Title != "write tests" {
		t.Errorf("expected task title %q, got %q", "write tests", tasks[0].Title)
	}
}

// A task created from the list must appear as a row without a manual
// refresh — the reported symptom was the first task never showing up.
func TestCreatedTaskAppearsAsRow(t *testing.T) {
	app := newTestApp(t, 80, 24, "first")

	rowsBefore := len(app.rows)

	pressKey(t, app, "t")
	for _, r := range "subtask" {
		send(t, app, runeKey(r))
	}
	send(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(app.rows) != rowsBefore+1 {
		t.Fatalf("expected a new task row, rows went %d → %d", rowsBefore, len(app.rows))
	}
	if app.rows[len(app.rows)-1].kind != rowTask {
		t.Errorf("expected the new row to be a task row")
	}
	if !strings.Contains(app.screen(), "subtask") {
		t.Errorf("new task not rendered:\n%s", app.screen())
	}
}

// In API mode a tick reads the API again. One arriving mid-move waits for the
// next tick, because a read replaces the rows being moved. It still re-arms, or
// the board stops reading the API for the rest of the session.
func TestAnAPIRereadWaitsOutAMoveAndLandsOnTheNextTick(t *testing.T) {
	app := newTestApp(t, 80, 24, "here at launch")
	app.where = Where{APIHost: "projects.example.com"}
	projects, err := app.backend.ListProjectsByStatus(model.StatusActive)
	if err != nil || len(projects) == 0 {
		t.Fatalf("listing projects: %v", err)
	}
	if _, err := app.backend.CreateItem(model.CreateProjectItem{Title: "written elsewhere", ProjectIDs: []string{projects[0].ID}}); err != nil {
		t.Fatal(err)
	}
	shown := func() bool {
		return slices.ContainsFunc(app.items, func(item model.ProjectItemInProject) bool { return item.Title == "written elsewhere" })
	}

	app.appMode = modeMove
	_, cmd := app.Update(rereadTickMsg{})
	if cmd == nil {
		t.Fatal("a tick during a move returned no command, so the rereads stopped")
	}
	drain(t, app, cmd, 16)
	if shown() {
		t.Fatal("a tick during a move read the API under the rows being moved")
	}

	app.appMode = modeNormal
	send(t, app, rereadTickMsg{})
	if !shown() {
		t.Error("the tick after the move did not read the API")
	}
}

// Each flash lasts its full duration. A second flash raised before the first
// one's timer fires must outlive that timer.
func TestOlderFlashTimerLeavesNewerFlashShowing(t *testing.T) {
	app := newTestApp(t, 80, 24, "Alpha")

	app.flash("first")
	firstClear := clearStatusMsg{seq: app.flashSeq}
	app.flash("second")
	secondClear := clearStatusMsg{seq: app.flashSeq}

	send(t, app, firstClear)
	if app.statusMsg != "second" {
		t.Fatalf("the first flash's timer cleared the second flash: %q", app.statusMsg)
	}
	send(t, app, secondClear)
	if app.statusMsg != "" {
		t.Errorf("the second flash's own timer left it showing: %q", app.statusMsg)
	}
}

// The row is where the handle has to be legible: the id column was 8 characters
// of hex tail, which resolves but which nobody can read back off the screen.
func TestItemRowShowsTheNumber(t *testing.T) {
	app := newTestApp(t, 100, 30, "First item", "Second item")

	view := app.screen()

	items, err := app.backend.ListAllItemsIncludingArchived()
	if err != nil {
		t.Fatalf("listing items: %v", err)
	}
	for _, item := range items {
		if item.Number == nil {
			t.Fatalf("item %q has no number in a database", item.Title)
		}
		if !strings.Contains(view, strconv.Itoa(*item.Number)) {
			t.Errorf("view is missing the handle %d for %q", *item.Number, item.Title)
		}
		if strings.Contains(view, shortID(item.ID)) {
			t.Errorf("view still shows the UUID tail %s for %q", shortID(item.ID), item.Title)
		}
	}
}

// --- Project status ---

func keys(t *testing.T, app *App, presses ...string) {
	t.Helper()
	for _, press := range presses {
		for _, r := range press {
			send(t, app, runeKey(r))
		}
	}
}

func TestCompletedProjectLeavesTheProjectPane(t *testing.T) {
	app := newTestApp(t, 100, 30)
	app.activePane = projectPane

	send(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.appMode != modeReader || app.projectDetail == nil {
		t.Fatalf("expected the reader on the project, got mode %v", app.appMode)
	}
	keys(t, app, "c")

	if len(app.projects) != 0 {
		t.Errorf("completing a project must take it out of the pane, still have %d", len(app.projects))
	}
}

func TestShowClosedTogglesTheClosedProjectsBackIn(t *testing.T) {
	app := newTestApp(t, 100, 30)
	app.activePane = projectPane
	send(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	keys(t, app, "c")
	send(t, app, tea.KeyPressMsg{Code: tea.KeyEscape})

	keys(t, app, "C")

	if len(app.projects) != 1 {
		t.Fatalf("C must bring closed projects back, got %d", len(app.projects))
	}
	if app.projects[0].Status != model.StatusCompleted {
		t.Errorf("status = %q, want done", app.projects[0].Status)
	}
	if !strings.Contains(app.screen(), "completed") {
		t.Errorf("the row must be labeled with its status:\n%s", app.screen())
	}

	keys(t, app, "C")
	if len(app.projects) != 0 {
		t.Errorf("C again must hide them, got %d", len(app.projects))
	}
}

// A shelved project is not closed, but it is not open either: it leaves the
// pane, and its reader offers reopen and refuses complete.
func TestShelvedProjectOffersOnlyReopen(t *testing.T) {
	app := newTestApp(t, 100, 30)
	app.activePane = projectPane
	send(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	keys(t, app, "S")
	send(t, app, tea.KeyPressMsg{Code: tea.KeyEscape})

	if len(app.projects) != 0 {
		t.Fatalf("shelving must take the project out of the pane, still have %d", len(app.projects))
	}

	keys(t, app, "C")
	if len(app.projects) != 1 || app.projects[0].Status != model.StatusSomeday {
		t.Fatalf("C must bring the shelved project back as someday, got %v", app.projects)
	}

	send(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	keys(t, app, "c")
	if app.projects[0].Status != model.StatusSomeday {
		t.Errorf("c on a shelved project must be refused, status = %q", app.projects[0].Status)
	}

	keys(t, app, "r")
	if app.projects[0].Status != model.StatusActive {
		t.Errorf("r must reopen a shelved project, status = %q", app.projects[0].Status)
	}
}

// A reason is required, so dropping is a prompt rather than a keypress.
func TestDroppingAProjectAsksWhy(t *testing.T) {
	app := newTestApp(t, 100, 30)
	app.activePane = projectPane
	send(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})

	keys(t, app, "x")

	if app.appMode != modeDropProject {
		t.Fatalf("expected the drop prompt, got mode %v", app.appMode)
	}
	if len(app.projects) != 1 {
		t.Error("nothing may be dropped before the reason is given")
	}

	keys(t, app, "Go covers it")
	send(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(app.projects) != 0 {
		t.Fatalf("the drop did not land, still have %d projects", len(app.projects))
	}
	app.showClosed = true
	send(t, app, fetchProjectsCmd(app.backend, app.projectStatusFilter())())
	if got := app.projects[0].StatusReason; got == nil || *got != "Go covers it" {
		t.Errorf("status_reason = %v, want the typed reason", got)
	}
}

// --- Item pane scrolling ---

// assertPaneInvariants checks the two properties the whole layout rests on:
// the view fits the window it was given, and the row under the cursor is in it.
// They hold at every size after every key, so one loop over sizes and
// keystrokes covers a surface no manual pass can.
//
// The cursor check asserts on the line the renderer produced rather than on the
// item's title, because a title wider than the pane is legitimately truncated —
// what must not happen is the window being placed where that line is not.
func assertPaneInvariants(t *testing.T, app *App, context string) {
	t.Helper()
	view := app.screen()
	if got := lipgloss.Height(view); got > app.height {
		t.Fatalf("%s: the view is %d lines in a %d-line window:\n%s", context, got, app.height, view)
	}
	if app.currentRow() == nil {
		return
	}
	block := app.renderRowBlock(app.rowCursor, app.width-app.projectPaneWidth()-4)
	if app.rowHasLeadingBlank(app.rowCursor) {
		block = block[1:]
	}
	if !strings.Contains(view, block[0]) {
		t.Fatalf("%s: cursor sits on row %d/%d (scroll %d) but the pane does not render its line %q:\n%s",
			context, app.rowCursor+1, len(app.rows), app.rowScroll, block[0], view)
	}
}

// chainedApp is an app whose items each depend on the next, so every item but
// the last carries a blocker line under its row.
func chainedApp(t *testing.T, width, height, n int) *App {
	t.Helper()
	titles := make([]string, n)
	for i := range n {
		titles[i] = "item-" + strconv.Itoa(i)
	}
	app := newTestApp(t, width, height, titles...)
	for i := range n - 1 {
		dependsOn(t, app, titles[i], titles[i+1])
	}
	send(t, app, app.fetchItems()())
	return app
}

// The pane scrolls by row index and renders by line. A row that renders into
// more than one line — a blocker, a group header — makes the two disagree:
// syncScroll believes the cursor is inside the window while the render runs out
// of lines before reaching it, so pressing j walks the cursor off the bottom and
// the page never follows.
func TestCursorStaysVisibleScrollingPastBlockers(t *testing.T) {
	for _, height := range []int{10, 14, 24, 40} {
		t.Run("height="+strconv.Itoa(height), func(t *testing.T) {
			app := chainedApp(t, 80, height, 12)
			assertPaneInvariants(t, app, "at the top")
			for range len(app.rows) - 1 {
				pressKey(t, app, "j")
				assertPaneInvariants(t, app, "after j")
			}
			for range len(app.rows) - 1 {
				pressKey(t, app, "k")
				assertPaneInvariants(t, app, "after k")
			}
		})
	}
}

// The grouped view adds a header and a blank separator to the first row of
// every group, which is the same divergence between row count and line count
// that notes cause — and the view where a long list is most likely to be read.
func TestCursorStaysVisibleInTheGroupedView(t *testing.T) {
	app := newTestApp(t, 80, 16)
	for p := range 4 {
		project, err := app.backend.CreateProject(model.CreateProject{Name: "project-" + strconv.Itoa(p)})
		if err != nil {
			t.Fatalf("creating project: %v", err)
		}
		for i := range 3 {
			if _, err := app.backend.CreateItem(model.CreateProjectItem{
				Title:      "p" + strconv.Itoa(p) + "-item-" + strconv.Itoa(i),
				ProjectIDs: []string{project.ID},
			}); err != nil {
				t.Fatalf("creating item: %v", err)
			}
		}
	}
	send(t, app, fetchProjectsCmd(app.backend, app.projectStatusFilter())())
	app.showingAll = true
	send(t, app, fetchAllItemsCmd(app.backend, app.projects, app.filter)())
	app.activePane = itemPane

	if !app.isGroupedView() {
		t.Fatal("expected the grouped All Items view")
	}
	assertPaneInvariants(t, app, "at the top")
	for range len(app.rows) - 1 {
		pressKey(t, app, "j")
		assertPaneInvariants(t, app, "after j")
	}
}

// addTask creates a sub-task on the first item and refreshes. Typing it in
// through the prompt is the same result at a hundred keystrokes, each of which
// waits out a flash timer.
func addTask(t *testing.T, app *App, title string) {
	t.Helper()
	if _, err := app.backend.CreateTask(app.items[0].ID, model.CreateProjectItemTask{Title: title}); err != nil {
		t.Fatalf("creating task: %v", err)
	}
	send(t, app, app.fetchItems()())
}

// The measure pass and the draw pass are separate so that showing twenty rows
// does not cost the styling of three hundred. They have to agree exactly: a
// height that is one line off from what gets drawn is the original scroll bug
// with extra steps.
func TestLayoutHeightMatchesWhatGetsRendered(t *testing.T) {
	for _, width := range []int{20, 40, 80, 200} {
		app := chainedApp(t, width+30, 40, 6)
		addTask(t, app, "a task title that is itself longer than a narrow pane")

		layout := app.itemPaneLayout(width)
		for i := range app.rows {
			if got, want := len(app.renderRowBlock(i, width)), layout.height[i]; got != want {
				t.Errorf("width %d row %d: measured %d lines, rendered %d", width, i, want, got)
			}
		}
	}
}

// Every line the pane emits has to fit the column it was handed, or lipgloss
// wraps it at the border and the layout loses a line it never counted. Real
// titles and project names are both longer than a pane routinely is.
func TestNoRenderedLineExceedsThePaneWidth(t *testing.T) {
	long := "a title that is very considerably longer than any reasonable pane will ever be, by design"
	for _, width := range []int{18, 25, 40, 80} {
		app := newTestApp(t, width+30, 40, long, "short")
		dependsOn(t, app, "short", long)
		addTask(t, app, long)

		for i := range app.rows {
			for _, line := range app.renderRowBlock(i, width) {
				if got := lipgloss.Width(line); got > width {
					t.Errorf("width %d row %d: line is %d wide: %q", width, i, got, line)
				}
			}
		}
	}
}

// --- Project pane ---

// A total alone cannot tell a project with nothing left in it from one nobody
// has started, which is the question the pane exists to answer at a glance.
func TestProjectPaneShowsOpenAndDoneCounts(t *testing.T) {
	app := newTestApp(t, 100, 30, "first", "second", "third")

	pressKey(t, app, " ")

	projects, err := app.backend.ListProjectsByStatus(model.StatusActive)
	if err != nil {
		t.Fatalf("listing projects: %v", err)
	}
	if projects[0].ItemCount != 3 || projects[0].CompletedCount != 1 {
		t.Fatalf("counts = %d items / %d done, want 3/1", projects[0].ItemCount, projects[0].CompletedCount)
	}
	if got := projects[0].OpenCount; got != 2 {
		t.Errorf("OpenCount = %d, want 2", got)
	}

	send(t, app, fetchProjectsCmd(app.backend, app.projectStatusFilter())())
	if view := app.screen(); !strings.Contains(view, "○2") || !strings.Contains(view, "✓1") {
		t.Errorf("project row does not show the open/done split:\n%s", view)
	}
}

// The pane is where a name can be longer than the column it is drawn in, and
// the count glyphs are multi-byte — truncating the finished line by byte cut
// one in half and corrupted the row.
func TestProjectPaneNeverOverflowsItsBox(t *testing.T) {
	app := newTestApp(t, 60, 12, "only")
	for p := range 20 {
		if _, err := app.backend.CreateProject(model.CreateProject{
			Name: "a-rather-long-project-name-" + strconv.Itoa(p),
		}); err != nil {
			t.Fatalf("creating project: %v", err)
		}
	}
	send(t, app, fetchProjectsCmd(app.backend, app.projectStatusFilter())())
	app.activePane = projectPane

	for range 20 {
		pressKey(t, app, "j")
		view := app.screen()
		if got := lipgloss.Height(view); got > 12 {
			t.Fatalf("view rendered %d lines in a 12-line window:\n%s", got, view)
		}
		if !strings.Contains(view, app.projects[app.projectCursor].Name[:10]) {
			t.Fatalf("cursor on %q but the pane does not render it:\n%s",
				app.projects[app.projectCursor].Name, view)
		}
	}
}

func TestCancellingTheDropPromptLeavesTheProjectAlone(t *testing.T) {
	app := newTestApp(t, 100, 30)
	app.activePane = projectPane
	send(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	keys(t, app, "x")

	send(t, app, tea.KeyPressMsg{Code: tea.KeyEscape})

	if app.appMode != modeReader {
		t.Errorf("Esc must return to the reader it came from, got mode %v", app.appMode)
	}
	if len(app.projects) != 1 {
		t.Error("canceling must not drop anything")
	}
}

// A terminal that honors Bubble Tea's keyboard requests, such as tmux with
// extended-keys on, reports these three apart from Esc, Enter and Tab.
func TestKeysSomeTerminalsReportApartActAsEscEnterAndTab(t *testing.T) {
	app := newTestApp(t, 100, 30)

	keys(t, app, "?")
	send(t, app, tea.KeyPressMsg{Code: '[', Mod: tea.ModCtrl})
	if app.appMode != modeNormal {
		t.Errorf("ctrl+[ left mode %v, want help closed", app.appMode)
	}

	before := app.activePane
	send(t, app, tea.KeyPressMsg{Code: 'i', Mod: tea.ModCtrl})
	if app.activePane == before {
		t.Error("ctrl+i did not switch panes")
	}

	keys(t, app, "a", "Typed")
	send(t, app, tea.KeyPressMsg{Code: 'm', Mod: tea.ModCtrl})
	if app.appMode != modeNormal {
		t.Errorf("ctrl+m left mode %v, want the prompt submitted", app.appMode)
	}
}

// A person copies a whole line, newline included, and pastes it into a filter. A
// paste arrives as one message rather than as keys, so each input has to be handed
// it, and a filter has to narrow on it as it does on typing.
func TestPasteGoesToTheFocusedInputAndNarrowsItsFilter(t *testing.T) {
	app := newTestApp(t, 120, 30, "serve", "schema", "unrelated-a", "unrelated-b")
	dependsOn(t, app, "serve", "schema")
	dependsOn(t, app, "unrelated-a", "unrelated-b")
	paste := func(text string) {
		t.Helper()
		send(t, app, tea.PasteMsg{Content: text})
	}
	escape := tea.KeyPressMsg{Code: tea.KeyEscape}

	before := app.screen()
	paste("nothing focused")
	if app.screen() != before {
		t.Error("a paste with no input focused must change nothing")
	}

	keys(t, app, "a")
	app.errorMsg = "stale"
	paste("Pasted title")
	if got := app.titleInput.Value(); got != "Pasted title" {
		t.Errorf("add prompt holds %q, want the pasted title", got)
	}
	if app.errorMsg != "" {
		t.Errorf("a paste clears a transient message as a key does, got %q", app.errorMsg)
	}
	send(t, app, escape)

	app.activePane = itemPane
	keys(t, app, "b", "/")
	paste("unrelated-a\n")
	if got := app.depFilter.Value(); got != "unrelated-a" {
		t.Errorf("dependency filter holds %q, want the pasted text", got)
	}
	if strings.Contains(app.screen(), "schema") {
		t.Errorf("dependency filter did not narrow on the paste:\n%s", app.screen())
	}
	send(t, app, escape)
	send(t, app, escape)

	openTree(t, app)
	keys(t, app, "/")
	paste("unrelated-b")
	if got := forestTitles(app); len(got) != 2 || got[1] != "unrelated-b" {
		t.Errorf("tree search narrowed to %v, want the pasted match and the row above it", got)
	}
}
