package tui

import (
	"fmt"
	"os"
	"testing"
	"time"

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

		app := NewApp(service.New(sqlite.New(database)), nil, Where{DatabasePath: path}, 2*time.Minute)
		send(t, app, tea.WindowSizeMsg{Width: width, Height: height})
		send(t, app, app.Init()())
		app.activePane = itemPane

		walk := func(label string) {
			app.rowCursor, app.rowScroll = 0, 0
			for i := range len(app.rows) {
				if i > 0 {
					pressKey(t, app, "j")
				}
				assertPaneInvariants(t, app, fmt.Sprintf("%dx%d %s", width, height, label))
			}
		}

		for pi := range app.projects {
			app.projectCursor, app.showingAll = pi, false
			send(t, app, app.fetchItems()())
			walk(app.projects[pi].Name)
		}

		app.showingAll = true
		send(t, app, fetchAllItemsCmd(app.backend, app.projects, app.filter)())
		walk("All Items")
	}
}
