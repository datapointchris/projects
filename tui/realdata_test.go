package tui

import (
	"fmt"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/datapointchris/projects/service"
	"github.com/datapointchris/projects/store/sqlite"
)

// The layout bugs that reached a human all needed content no fixture had:
// titles longer than a pane, notes hundreds of lines deep, a project name that
// filled the blocker line on its own. So this walks the real database instead —
// every project, every row, at six terminal sizes — and asserts the two things
// that must hold everywhere: the pane draws inside the box it was handed, and
// the row under the cursor is on screen.
//
// It skips without a database rather than shipping a fixture, because a fixture
// is exactly the synthetic content that missed these in the first place. Set
// PROJECTS_DB to run it; .envrc already points it at the dev database.
func TestRealDatabaseSurvivesEverySizeAndProject(t *testing.T) {
	path := os.Getenv("PROJECTS_DB")
	if path == "" {
		t.Skip("PROJECTS_DB is not set: nothing real to walk")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("PROJECTS_DB=%s is not readable: %v", path, err)
	}

	database, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	t.Cleanup(func() { _ = database.Close() })

	sizes := [][2]int{{40, 10}, {60, 12}, {80, 24}, {100, 30}, {120, 40}, {200, 50}}
	for _, size := range sizes {
		width, height := size[0], size[1]

		app := NewApp(service.New(sqlite.New(database)), Where{DatabasePath: path})
		send(t, app, tea.WindowSizeMsg{Width: width, Height: height})
		send(t, app, app.Init()())
		app.activePane = itemPane

		// Each list is walked twice: with the notes closed, then with every note open in place.
		walk := func(label string) {
			for _, open := range []bool{false, true} {
				for _, item := range app.items {
					app.opened[item.ID] = open
				}
				app.rowCursor, app.rowScroll, app.scrolled = 0, 0, false
				for i := range len(app.rows) {
					if i > 0 {
						pressKey(t, app, "j")
					}
					assertPaneInvariants(t, app, fmt.Sprintf("%dx%d %s, notes open %v", width, height, label, open))
				}
			}
		}

		for pi := range app.projects {
			app.projectCursor = pi
			send(t, app, app.fetchItems()())
			walk(app.projects[pi].Name)
		}

		send(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
		walk("All items")
	}
}
