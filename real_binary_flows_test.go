package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/datapointchris/tuidrive"

	"github.com/datapointchris/todoui/backend"
	"github.com/datapointchris/todoui/db"
	"github.com/datapointchris/todoui/model"
)

// Each flow replays testdata/flows/<script> against the built binary on a pty, from
// a freshly seeded database, and compares every frame it names with the goldens
// under testdata/TestRealBinaryFlows/<flow>. TODOUI_DB is relative to the program's
// working directory, which is the test's temporary directory, so the status bar
// reads the same on every run and no flow can reach a real database. Write the
// goldens with TUIDRIVE_UPDATE=1.
func TestRealBinaryFlows(t *testing.T) {
	binary := tuidrive.Build(t, ".")
	editor, err := filepath.Abs("testdata/flows/append-line-editor.sh")
	if err != nil {
		t.Fatal(err)
	}

	for _, flow := range []struct {
		name, script string
		cols, rows   int
	}{
		{name: "browse", script: "browse.keys"},
		{name: "reader", script: "reader.keys"},
		{name: "add-project-and-item", script: "add-project-and-item.keys"},
		{name: "add-task", script: "add-task.keys"},
		{name: "dependency-tree", script: "dependency-tree.keys"},
		{name: "undo", script: "undo.keys"},
		{name: "search", script: "search.keys"},
		{name: "edit-note", script: "edit-note.keys"},
		{name: "help", script: "help.keys", rows: 60},
		{name: "help-taller-than-the-window", script: "help.keys"},
		{name: "narrow-window", script: "narrow-window.keys", cols: 50, rows: 14},
	} {
		t.Run(flow.name, func(t *testing.T) {
			dir := t.TempDir()
			seedFixtureDatabase(t, filepath.Join(dir, "todoui.db"))
			term := tuidrive.Start(t, tuidrive.Program{
				Path: binary,
				Dir:  dir,
				Env:  map[string]string{"TODOUI_DB": "todoui.db", "EDITOR": editor},
				Cols: flow.cols,
				Rows: flow.rows,
			})
			term.RunScript(filepath.Join("testdata", "flows", flow.script))
		})
	}
}

// seedFixtureDatabase writes through the backend the TUI itself writes through, so
// the fixture holds what todoui's own rules allow. A note here is as long as a real
// one, because one-line notes are what kept layout bugs out of earlier fixtures.
func seedFixtureDatabase(t *testing.T, path string) {
	t.Helper()
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	seed := fixtureSeeder{t: t, backend: backend.NewLocalBackend(database, backend.AssigningItemNumbers())}

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
	backend *backend.LocalBackend
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
