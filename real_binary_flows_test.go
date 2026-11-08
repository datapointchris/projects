package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/datapointchris/tuidrive"

	"github.com/datapointchris/projects/api/routes"
	"github.com/datapointchris/projects/cli"
	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/service"
	"github.com/datapointchris/projects/store/sqlite"
)

// Each flow replays testdata/flows/<script> against the built binary on a pty, from
// a freshly seeded database, and compares every frame it names with the goldens
// under testdata/TestRealBinaryFlows/<flow>. PROJECTS_DB is relative to the program's
// working directory, which is the test's temporary directory, so the status bar
// reads the same on every run and no flow can reach a real database. The api-mode
// flow reads its seeded database through the API's routes over HTTP, and must open
// none of its own. Write the goldens with TUIDRIVE_UPDATE=1.
func TestRealBinaryFlows(t *testing.T) {
	binary := tuidrive.Build(t, ".")
	editor, err := filepath.Abs("testdata/flows/append-line-editor.sh")
	if err != nil {
		t.Fatal(err)
	}

	for _, flow := range []struct {
		name, script string
		cols, rows   int
		api          bool
	}{
		{name: "browse", script: "browse.keys"},
		{name: "views", script: "views.keys"},
		{name: "filter", script: "filter.keys"},
		{name: "move", script: "move.keys"},
		{name: "detail-in-place", script: "detail-in-place.keys"},
		{name: "reader", script: "reader.keys"},
		{name: "add-project-and-item", script: "add-project-and-item.keys"},
		{name: "add-task", script: "add-task.keys"},
		{name: "dependency-tree", script: "dependency-tree.keys"},
		{name: "undo", script: "undo.keys"},
		{name: "edit-note", script: "edit-note.keys"},
		{name: "help-taller-than-the-window", script: "help.keys"},
		{name: "narrow-window", script: "narrow-window.keys", cols: 50, rows: 14},
		{name: "api-mode", script: "api-mode.keys", api: true},
	} {
		t.Run(flow.name, func(t *testing.T) {
			dir := t.TempDir()
			if flow.api {
				served := filepath.Join(t.TempDir(), "served.db")
				seedFixtureDatabase(t, served)
				writeAPIConfig(t, dir, serveOverHTTP(t, served))
			} else {
				seedFixtureDatabase(t, filepath.Join(dir, "projects.db"))
			}
			term := tuidrive.Start(t, tuidrive.Program{
				Path: binary,
				Args: []string{"tui"},
				Dir:  dir,
				Env: map[string]string{
					"PROJECTS_DB":     "projects.db",
					"EDITOR":          editor,
					"XDG_CONFIG_HOME": filepath.Join(dir, "config"),
				},
				Cols: flow.cols,
				Rows: flow.rows,
			})
			term.RunScript(filepath.Join("testdata", "flows", flow.script))
			if _, err := os.Stat(filepath.Join(dir, "projects.db")); flow.api && err == nil {
				t.Error("API mode opened a database")
			}
		})
	}
}

// serveOverHTTP is the API's own routes over the database at path, reached the
// way a deployed API is. It checks no token, as the API behind a proxy that
// already did.
func serveOverHTTP(t *testing.T, path string) string {
	t.Helper()
	database, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(routes.New(cli.LocalService(database, ""), quiet))
	t.Cleanup(server.Close)
	return server.URL
}

// writeAPIConfig puts the program in API mode, with a token_command standing in
// for a login.
func writeAPIConfig(t *testing.T, dir, api string) {
	t.Helper()
	path := filepath.Join(dir, "config", "projects", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("api = %q\ntoken_command = [\"printf\", %q]\n", api, `{"access_token":"stand-in","token_type":"Bearer"}`)
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
}

// seedFixtureDatabase writes through the backend the TUI writes through in database mode,
// so the fixture holds what the tool's own rules allow. The export note runs long,
// because a one-line note hides layout bugs.
func seedFixtureDatabase(t *testing.T, path string) {
	t.Helper()
	database, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	seed := fixtureSeeder{t: t, backend: cli.LocalService(database, filepath.Join(filepath.Dir(path), "repos.json"))}

	blog := seed.project("Move the blog to plain files", fixtureText(t, "blog-project-description.md"))
	faucet := seed.project("Replace the kitchen faucet", "")
	trip := seed.project("Plan the Lisbon trip", "")

	export := seed.item("Export posts from the old CMS", fixtureText(t, "export-posts-note.md"), blog)
	seed.task(export, "Dump the database", true)
	seed.task(export, "Convert HTML to markdown", false)
	seed.task(export, "Download the images", false)
	theme := seed.item("Pick a theme", "", blog)
	seed.update(theme, model.UpdateProjectItem{Completed: ptr(true)})
	redirects := seed.item("Set up redirects for the old URLs", "", blog)
	seed.dependsOn(redirects, export)
	dns := seed.item("Point DNS at the new host", "", blog)
	seed.dependsOn(dns, redirects)
	quotes := seed.item("Compare hosting quotes", "", blog)
	seed.update(quotes, model.UpdateProjectItem{Archived: ptr(true)})

	buy := seed.item("Buy the faucet", "Brushed nickel, with a pull-down spray and a single handle.", faucet)
	swap := seed.item("Shut off the water and swap it", "", faucet)
	seed.dependsOn(swap, buy)
	seed.item("Ask Sam for a plumber and a place to stay", "", faucet, trip)

	writeUp := seed.item("Write up the trip on the new blog", "", trip)
	seed.dependsOn(writeUp, dns)
	seed.item("Read every neighborhood guide and write down which three are worth the walk, the tram ride and the hills", "", trip)
	for day := 1; day <= 28; day++ {
		seed.item(fmt.Sprintf("Day %d: walk the next neighborhood", day), "", trip)
	}
}

// fixtureSeeder ends the test on the first write the backend refuses.
type fixtureSeeder struct {
	t       *testing.T
	backend *service.Service
}

func (s fixtureSeeder) project(name, description string) string {
	s.t.Helper()
	input := model.CreateProject{Name: name}
	if description != "" {
		input.Description = &description
	}
	project, err := s.backend.CreateProject(input)
	if err != nil {
		s.t.Fatalf("creating project %q: %v", name, err)
	}
	return project.ID
}

func (s fixtureSeeder) item(title, notes string, projectIDs ...string) string {
	s.t.Helper()
	input := model.CreateProjectItem{Title: title, ProjectIDs: projectIDs}
	if notes != "" {
		input.Notes = &notes
	}
	item, err := s.backend.CreateItem(input)
	if err != nil {
		s.t.Fatalf("creating item %q: %v", title, err)
	}
	return item.ID
}

func (s fixtureSeeder) task(itemID, title string, completed bool) {
	s.t.Helper()
	task, err := s.backend.CreateTask(itemID, model.CreateProjectItemTask{Title: title})
	if err != nil {
		s.t.Fatalf("creating task %q: %v", title, err)
	}
	if !completed {
		return
	}
	if _, err := s.backend.UpdateTask(itemID, task.ID, model.UpdateProjectItemTask{Completed: &completed}); err != nil {
		s.t.Fatalf("completing task %q: %v", title, err)
	}
}

func (s fixtureSeeder) update(itemID string, input model.UpdateProjectItem) {
	s.t.Helper()
	if _, err := s.backend.UpdateItem(itemID, input); err != nil {
		s.t.Fatalf("updating item %s: %v", itemID, err)
	}
}

func (s fixtureSeeder) dependsOn(itemID, blockerID string) {
	s.t.Helper()
	if err := s.backend.AddDependency(itemID, blockerID); err != nil {
		s.t.Fatalf("linking %s to %s: %v", itemID, blockerID, err)
	}
}

func fixtureText(t *testing.T, name string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", "fixture", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func ptr[T any](v T) *T { return &v }
