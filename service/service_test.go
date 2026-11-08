package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store/postgres"
	"github.com/datapointchris/projects/store/postgres/postgrestest"
	"github.com/datapointchris/projects/store/sqlite"
)

// testStoreEnv names the store this suite's rules run over, so one rule is proved on
// both stores by one test. CI runs the suite once for each.
const testStoreEnv = "PROJECTS_TEST_STORE"

func runsOnPostgres() bool {
	return os.Getenv(testStoreEnv) == "postgres"
}

func newTestBackend(t *testing.T) *Service {
	t.Helper()
	if runsOnPostgres() {
		return New(postgres.New(postgrestest.Pool(t)))
	}
	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(sqlite.New(database))
}

// Each store seeds its lookup rows its own way. A kind one holds and the other lacks
// is accepted on one and refused on the other.
func TestEachStoreHoldsTheKnownKindsAndStatuses(t *testing.T) {
	b := newTestBackend(t)
	ctx := context.Background()

	kinds, err := b.store.ListProjectKinds(ctx)
	if err != nil {
		t.Fatalf("listing kinds: %v", err)
	}
	statuses, err := b.store.ListProjectStatuses(ctx)
	if err != nil {
		t.Fatalf("listing statuses: %v", err)
	}
	if want := slices.Sorted(slices.Values(model.ProjectKinds)); !slices.Equal(kinds, want) {
		t.Errorf("the store holds kinds %v, want %v", kinds, want)
	}
	if want := slices.Sorted(slices.Values(model.ProjectStatuses)); !slices.Equal(statuses, want) {
		t.Errorf("the store holds statuses %v, want %v", statuses, want)
	}
}

func mustCreateProject(t *testing.T, b *Service, name string) *model.Project {
	t.Helper()
	p, err := b.CreateProject(model.CreateProject{Name: name})
	if err != nil {
		t.Fatalf("creating project %q: %v", name, err)
	}
	return p
}

func mustCreateItem(t *testing.T, b *Service, input model.CreateProjectItem) *model.ProjectItemDetail {
	t.Helper()
	item, err := b.CreateItem(input)
	if err != nil {
		t.Fatalf("creating item %q: %v", input.Title, err)
	}
	return item
}

func TestCreateAndListProjects(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	if p.Name != "work" {
		t.Errorf("expected name 'work', got %q", p.Name)
	}

	projects, err := b.ListProjectsByStatus(model.StatusActive)
	if err != nil {
		t.Fatalf("listing projects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	if projects[0].Name != "work" {
		t.Errorf("expected 'work', got %q", projects[0].Name)
	}
}

func TestGetProject(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	mustCreateItem(t, b, model.CreateProjectItem{Title: "task 1", ProjectIDs: []string{p.ID}})
	mustCreateItem(t, b, model.CreateProjectItem{Title: "task 2", ProjectIDs: []string{p.ID}})

	project, err := b.GetProject(p.ID)
	if err != nil {
		t.Fatalf("getting project: %v", err)
	}
	if project.Name != "work" {
		t.Errorf("expected 'work', got %q", project.Name)
	}
	if project.ItemCount != 2 {
		t.Errorf("expected item_count 2, got %d", project.ItemCount)
	}
}

func TestUpdateProject(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "old name")
	newName := "new name"
	newPos := 5

	updated, err := b.UpdateProject(p.ID, model.UpdateProject{Name: &newName, Position: &newPos})
	if err != nil {
		t.Fatalf("updating project: %v", err)
	}
	if updated.Name != "new name" {
		t.Errorf("expected 'new name', got %q", updated.Name)
	}
	if updated.Position != 5 {
		t.Errorf("expected position 5, got %d", updated.Position)
	}
}

func TestProjectDescription(t *testing.T) {
	b := newTestBackend(t)

	desc := "A project for work tasks"
	p, err := b.CreateProject(model.CreateProject{Name: "work", Description: &desc})
	if err != nil {
		t.Fatalf("creating project with description: %v", err)
	}
	if p.Description == nil || *p.Description != desc {
		t.Errorf("expected description %q, got %v", desc, p.Description)
	}

	project, err := b.GetProject(p.ID)
	if err != nil {
		t.Fatalf("getting project: %v", err)
	}
	if project.Description == nil || *project.Description != desc {
		t.Errorf("expected description %q after get, got %v", desc, project.Description)
	}
}

func TestCreateItemRequiresProject(t *testing.T) {
	b := newTestBackend(t)

	_, err := b.CreateItem(model.CreateProjectItem{
		Title:      "orphan task",
		ProjectIDs: []string{},
	})
	if !errors.Is(err, model.ErrNoProjects) {
		t.Errorf("expected ErrNoProjects, got %v", err)
	}
}

func TestCreateAndGetItem(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	notes := "some notes"
	item, err := b.CreateItem(model.CreateProjectItem{
		Title:      "fix bug",
		Notes:      &notes,
		ProjectIDs: []string{p.ID},
	})
	if err != nil {
		t.Fatalf("creating item: %v", err)
	}
	if item.Title != "fix bug" {
		t.Errorf("expected title 'fix bug', got %q", item.Title)
	}
	if item.Notes == nil || *item.Notes != "some notes" {
		t.Errorf("expected notes 'some notes', got %v", item.Notes)
	}
	if len(item.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(item.Projects))
	}
	if item.Projects[0].Name != "work" {
		t.Errorf("expected project 'work', got %q", item.Projects[0].Name)
	}
	if item.DependencyIDs == nil {
		t.Error("expected non-nil dependency_ids")
	}
	if len(item.DependencyIDs) != 0 {
		t.Errorf("expected 0 dependency_ids, got %d", len(item.DependencyIDs))
	}
}

func TestMultiProjectItem(t *testing.T) {
	b := newTestBackend(t)

	p1 := mustCreateProject(t, b, "work")
	p2 := mustCreateProject(t, b, "personal")

	item, err := b.CreateItem(model.CreateProjectItem{
		Title:      "cross-cutting task",
		ProjectIDs: []string{p1.ID, p2.ID},
	})
	if err != nil {
		t.Fatalf("creating multi-project item: %v", err)
	}
	if len(item.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(item.Projects))
	}

	workItems, _ := b.ListItemsByProject(p1.ID)
	personalItems, _ := b.ListItemsByProject(p2.ID)
	if len(workItems) != 1 {
		t.Errorf("expected 1 item in work, got %d", len(workItems))
	}
	if len(personalItems) != 1 {
		t.Errorf("expected 1 item in personal, got %d", len(personalItems))
	}
}

func TestANewItemJoinsTheBottomOfItsProject(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	first := mustCreateItem(t, b, model.CreateProjectItem{Title: "first", ProjectIDs: []string{p.ID}})
	mustCreateItem(t, b, model.CreateProjectItem{Title: "second", ProjectIDs: []string{p.ID}})
	if err := b.ReorderItem(first.ID, p.ID, 9); err != nil {
		t.Fatalf("reordering: %v", err)
	}
	mustCreateItem(t, b, model.CreateProjectItem{Title: "third", ProjectIDs: []string{p.ID}})

	items, err := b.ListItemsByProject(p.ID)
	if err != nil {
		t.Fatalf("listing items: %v", err)
	}
	titles := make([]string, len(items))
	for i, item := range items {
		titles[i] = item.Title
	}
	if got, want := strings.Join(titles, ","), "second,first,third"; got != want {
		t.Errorf("order = %s, want %s", got, want)
	}
}

func TestRemoveFromProjectPreservesLastProject(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "only")
	item := mustCreateItem(t, b, model.CreateProjectItem{
		Title:      "task",
		ProjectIDs: []string{p.ID},
	})

	err := b.RemoveFromProject(item.ID, p.ID)
	if !errors.Is(err, model.ErrLastProject) {
		t.Errorf("expected ErrLastProject, got %v", err)
	}
}

func TestDependencyCycleDetection(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	t1 := mustCreateItem(t, b, model.CreateProjectItem{Title: "task 1", ProjectIDs: []string{p.ID}})
	t2 := mustCreateItem(t, b, model.CreateProjectItem{Title: "task 2", ProjectIDs: []string{p.ID}})
	t3 := mustCreateItem(t, b, model.CreateProjectItem{Title: "task 3", ProjectIDs: []string{p.ID}})

	if err := b.AddDependency(t1.ID, t2.ID); err != nil {
		t.Fatalf("adding dep t1->t2: %v", err)
	}
	if err := b.AddDependency(t2.ID, t3.ID); err != nil {
		t.Fatalf("adding dep t2->t3: %v", err)
	}

	err := b.AddDependency(t3.ID, t1.ID)
	if !errors.Is(err, model.ErrCyclicDependency) {
		t.Errorf("expected ErrCyclicDependency, got %v", err)
	}
}

func TestGetBlockers(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	t1 := mustCreateItem(t, b, model.CreateProjectItem{Title: "blocker", ProjectIDs: []string{p.ID}})
	t2 := mustCreateItem(t, b, model.CreateProjectItem{Title: "blocked", ProjectIDs: []string{p.ID}})

	_ = b.AddDependency(t2.ID, t1.ID)

	blockers, err := b.GetBlockers(t2.ID)
	if err != nil {
		t.Fatalf("getting blockers: %v", err)
	}
	if len(blockers) != 1 {
		t.Fatalf("expected 1 blocker, got %d", len(blockers))
	}
	if blockers[0].ID != t1.ID {
		t.Errorf("expected blocker ID %s, got %s", t1.ID, blockers[0].ID)
	}
}

func TestGetItemDependencyIDs(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	t1 := mustCreateItem(t, b, model.CreateProjectItem{Title: "dep 1", ProjectIDs: []string{p.ID}})
	t2 := mustCreateItem(t, b, model.CreateProjectItem{Title: "dep 2", ProjectIDs: []string{p.ID}})
	t3 := mustCreateItem(t, b, model.CreateProjectItem{Title: "main", ProjectIDs: []string{p.ID}})

	_ = b.AddDependency(t3.ID, t1.ID)
	_ = b.AddDependency(t3.ID, t2.ID)

	detail, err := b.GetItem(t3.ID)
	if err != nil {
		t.Fatalf("getting item: %v", err)
	}
	if len(detail.DependencyIDs) != 2 {
		t.Fatalf("expected 2 dependency_ids, got %d", len(detail.DependencyIDs))
	}
}

func TestUpdateItem(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "original", ProjectIDs: []string{p.ID}})

	newTitle := "updated"
	updated, err := b.UpdateItem(item.ID, model.UpdateProjectItem{Title: &newTitle})
	if err != nil {
		t.Fatalf("updating item: %v", err)
	}
	if updated.Title != "updated" {
		t.Errorf("expected title 'updated', got %q", updated.Title)
	}
}

func TestCompleteItem(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "task", ProjectIDs: []string{p.ID}})

	done := true
	updated, err := b.UpdateItem(item.ID, model.UpdateProjectItem{Completed: &done})
	if err != nil {
		t.Fatalf("completing item: %v", err)
	}
	if !updated.Completed {
		t.Error("expected item to be completed")
	}
}

func TestSearch(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	mustCreateItem(t, b, model.CreateProjectItem{Title: "fix auth bug", ProjectIDs: []string{p.ID}})
	mustCreateItem(t, b, model.CreateProjectItem{Title: "write tests", ProjectIDs: []string{p.ID}})

	results, err := b.Search("auth")
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "fix auth bug" {
		t.Errorf("expected 'fix auth bug', got %q", results[0].Title)
	}
}

func TestUndoCreateItem(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "will undo", ProjectIDs: []string{p.ID}})

	canUndo, _ := b.CanUndo()
	if !canUndo {
		t.Fatal("expected canUndo to be true")
	}

	result, err := b.Undo()
	if err != nil {
		t.Fatalf("undoing: %v", err)
	}
	if result.Description == "" {
		t.Error("expected non-empty undo description")
	}
	if result.EntityID != item.ID {
		t.Errorf("expected undo result for %s, got %s", item.ID, result.EntityID)
	}
	_, err = b.GetItem(item.ID)
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound after undo, got %v", err)
	}
}

func TestUndoCoversProjectCreates(t *testing.T) {
	b := newTestBackend(t)

	older := mustCreateProject(t, b, "older")
	unrelated := mustCreateItem(t, b, model.CreateProjectItem{Title: "unrelated", ProjectIDs: []string{older.ID}})
	newer := mustCreateProject(t, b, "newer")

	// The project create is the latest change, so undo reverses it and leaves
	// the item create before it alone.
	result, err := b.Undo()
	if err != nil {
		t.Fatalf("undoing: %v", err)
	}
	if result.EntityType != "project" || result.EntityID != newer.ID {
		t.Errorf("expected undo of project %s, got %s %s", newer.ID, result.EntityType, result.EntityID)
	}
	if _, err := b.GetItem(unrelated.ID); err != nil {
		t.Errorf("undo reached past the project create and destroyed an unrelated item: %v", err)
	}
	if _, err := b.GetProject(newer.ID); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected the project create to be reversed, got %v", err)
	}
}

func TestUndoRestoresDeletedProject(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "doomed")
	if err := b.DeleteProject(p.ID); err != nil {
		t.Fatalf("deleting project: %v", err)
	}

	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}
	restored, err := b.GetProject(p.ID)
	if err != nil {
		t.Fatalf("expected project back after undo: %v", err)
	}
	if restored.Name != "doomed" {
		t.Errorf("restored project is named %q, want doomed", restored.Name)
	}
}

func TestUndoWhenEmpty(t *testing.T) {
	b := newTestBackend(t)

	_, err := b.Undo()
	if err != model.ErrNothingToUndo {
		t.Errorf("expected ErrNothingToUndo, got %v", err)
	}
}

func TestProjectCountsPartitionItemCount(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	mustCreateItem(t, b, model.CreateProjectItem{Title: "active", ProjectIDs: []string{p.ID}})
	archived := mustCreateItem(t, b, model.CreateProjectItem{Title: "archived", ProjectIDs: []string{p.ID}})

	archiveTrue := true
	_, _ = b.UpdateItem(archived.ID, model.UpdateProjectItem{Archived: &archiveTrue})

	project, err := b.GetProject(p.ID)
	if err != nil {
		t.Fatalf("getting project: %v", err)
	}
	if project.ItemCount != 2 || project.OpenCount != 1 || project.CompletedCount != 0 {
		t.Errorf("counts = %d items, %d open, %d completed, want 2, 1, 0", project.ItemCount, project.OpenCount, project.CompletedCount)
	}
}

func TestTaskCRUD(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "main item", ProjectIDs: []string{p.ID}})

	// Create tasks
	task1, err := b.CreateTask(item.ID, model.CreateProjectItemTask{Title: "subtask 1"})
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}
	if task1.Title != "subtask 1" {
		t.Errorf("expected title 'subtask 1', got %q", task1.Title)
	}
	if task1.ItemID != item.ID {
		t.Errorf("expected item_id %s, got %s", item.ID, task1.ItemID)
	}

	task2, err := b.CreateTask(item.ID, model.CreateProjectItemTask{Title: "subtask 2"})
	if err != nil {
		t.Fatalf("creating task 2: %v", err)
	}

	// List tasks
	tasks, err := b.ListTasks(item.ID)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}

	// Update task
	newTitle := "updated subtask"
	updated, err := b.UpdateTask(item.ID, task1.ID, model.UpdateProjectItemTask{Title: &newTitle})
	if err != nil {
		t.Fatalf("updating task: %v", err)
	}
	if updated.Title != "updated subtask" {
		t.Errorf("expected 'updated subtask', got %q", updated.Title)
	}

	// Complete task
	done := true
	if _, err := b.UpdateTask(item.ID, task1.ID, model.UpdateProjectItemTask{Completed: &done}); err != nil {
		t.Fatalf("completing task: %v", err)
	}

	// Delete task
	if err := b.DeleteTask(item.ID, task2.ID); err != nil {
		t.Fatalf("deleting task: %v", err)
	}
	tasks, _ = b.ListTasks(item.ID)
	if len(tasks) != 1 {
		t.Errorf("expected 1 task after delete, got %d", len(tasks))
	}
}

func TestDeleteProject(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "doomed")
	if err := b.DeleteProject(p.ID); err != nil {
		t.Fatalf("deleting project: %v", err)
	}

	projects, _ := b.ListProjectsByStatus(model.StatusAll)
	if len(projects) != 0 {
		t.Errorf("expected 0 projects after delete, got %d", len(projects))
	}
}

func TestDeleteItem(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "doomed", ProjectIDs: []string{p.ID}})

	if err := b.DeleteItem(item.ID); err != nil {
		t.Fatalf("deleting item: %v", err)
	}

	_, err := b.GetItem(item.ID)
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}

	// Undo should restore the item
	_, err = b.Undo()
	if err != nil {
		t.Fatalf("undoing delete: %v", err)
	}
	restored, err := b.GetItem(item.ID)
	if err != nil {
		t.Fatalf("getting restored item: %v", err)
	}
	if restored.Title != "doomed" {
		t.Errorf("expected restored title 'doomed', got %q", restored.Title)
	}
}

func TestReorderItem(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	mustCreateItem(t, b, model.CreateProjectItem{Title: "first", ProjectIDs: []string{p.ID}})
	item2 := mustCreateItem(t, b, model.CreateProjectItem{Title: "second", ProjectIDs: []string{p.ID}})

	if err := b.ReorderItem(item2.ID, p.ID, 1); err != nil {
		t.Fatalf("reordering item: %v", err)
	}

	items, _ := b.ListItemsByProject(p.ID)
	for _, it := range items {
		if it.ID == item2.ID && it.Position != 1 {
			t.Errorf("expected position 1 for reordered item, got %d", it.Position)
		}
	}
}

func TestRemoveDependency(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	t1 := mustCreateItem(t, b, model.CreateProjectItem{Title: "blocker", ProjectIDs: []string{p.ID}})
	t2 := mustCreateItem(t, b, model.CreateProjectItem{Title: "blocked", ProjectIDs: []string{p.ID}})

	_ = b.AddDependency(t2.ID, t1.ID)

	if err := b.RemoveDependency(t2.ID, t1.ID); err != nil {
		t.Fatalf("removing dependency: %v", err)
	}

	blockers, _ := b.GetBlockers(t2.ID)
	if len(blockers) != 0 {
		t.Errorf("expected 0 blockers after remove, got %d", len(blockers))
	}
}

func TestListBlocked(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	blocker := mustCreateItem(t, b, model.CreateProjectItem{Title: "blocker", ProjectIDs: []string{p.ID}})
	blocked := mustCreateItem(t, b, model.CreateProjectItem{Title: "blocked", ProjectIDs: []string{p.ID}})
	mustCreateItem(t, b, model.CreateProjectItem{Title: "free", ProjectIDs: []string{p.ID}})

	_ = b.AddDependency(blocked.ID, blocker.ID)

	items, err := b.ListBlocked()
	if err != nil {
		t.Fatalf("ListBlocked: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 blocked item, got %d", len(items))
	}
	if items[0].ID != blocked.ID {
		t.Errorf("expected blocked item %s, got %s", blocked.ID, items[0].ID)
	}
}

func TestListArchived(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	mustCreateItem(t, b, model.CreateProjectItem{Title: "active", ProjectIDs: []string{p.ID}})
	archived := mustCreateItem(t, b, model.CreateProjectItem{Title: "archived", ProjectIDs: []string{p.ID}})

	archiveTrue := true
	_, _ = b.UpdateItem(archived.ID, model.UpdateProjectItem{Archived: &archiveTrue})

	items, err := b.ListArchived(p.ID)
	if err != nil {
		t.Fatalf("ListArchived: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 archived item, got %d", len(items))
	}
	if items[0].Title != "archived" {
		t.Errorf("expected 'archived', got %q", items[0].Title)
	}
}

func TestItemRepoRoundTripAndClear(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "tools")

	repo := "projects"
	item, err := b.CreateItem(model.CreateProjectItem{
		Title:      "fix the brief collision",
		Repo:       &repo,
		ProjectIDs: []string{p.ID},
	})
	if err != nil {
		t.Fatalf("creating item: %v", err)
	}
	if item.Repo == nil || *item.Repo != "projects" {
		t.Fatalf("expected repo 'projects', got %v", item.Repo)
	}

	fetched, err := b.GetItem(item.ID)
	if err != nil {
		t.Fatalf("getting item: %v", err)
	}
	if fetched.Repo == nil || *fetched.Repo != "projects" {
		t.Errorf("repo did not survive a read: %v", fetched.Repo)
	}

	// Updating an unrelated field must not clear the link.
	title := "renamed"
	if _, err := b.UpdateItem(item.ID, model.UpdateProjectItem{Title: &title}); err != nil {
		t.Fatalf("updating title: %v", err)
	}
	fetched, _ = b.GetItem(item.ID)
	if fetched.Repo == nil || *fetched.Repo != "projects" {
		t.Errorf("repo lost on unrelated update: %v", fetched.Repo)
	}

	empty := ""
	if _, err := b.UpdateItem(item.ID, model.UpdateProjectItem{Repo: &empty}); err != nil {
		t.Fatalf("clearing repo: %v", err)
	}
	fetched, _ = b.GetItem(item.ID)
	if fetched.Repo != nil {
		t.Errorf("empty string should unlink, got %v", fetched.Repo)
	}
}

func TestItemRepoIsNilForNonRepoWork(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "home")

	item, err := b.CreateItem(model.CreateProjectItem{
		Title:      "build the microwave cart",
		ProjectIDs: []string{p.ID},
	})
	if err != nil {
		t.Fatalf("creating item: %v", err)
	}
	if item.Repo != nil {
		t.Errorf("non-repo work must have a nil repo, got %v", item.Repo)
	}
}

func TestUndoRestoresDeletedItemMemberships(t *testing.T) {
	b := newTestBackend(t)

	work := mustCreateProject(t, b, "work")
	personal := mustCreateProject(t, b, "personal")
	item := mustCreateItem(t, b, model.CreateProjectItem{
		Title:      "doomed",
		ProjectIDs: []string{work.ID, personal.ID},
	})

	if err := b.DeleteItem(item.ID); err != nil {
		t.Fatalf("deleting item: %v", err)
	}
	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}

	// Memberships cascade on delete. Restoring only the row leaves an item
	// that belongs to no project, which the TUI cannot display at all.
	projects, err := b.GetItemProjects(item.ID)
	if err != nil {
		t.Fatalf("getting item projects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 memberships restored, got %d", len(projects))
	}
}

func TestUndoRestoresDeletedItemTasksAndDependencies(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	blocker := mustCreateItem(t, b, model.CreateProjectItem{Title: "blocker", ProjectIDs: []string{p.ID}})
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "doomed", ProjectIDs: []string{p.ID}})

	if _, err := b.CreateTask(item.ID, model.CreateProjectItemTask{Title: "step one"}); err != nil {
		t.Fatalf("creating task: %v", err)
	}
	if err := b.AddDependency(item.ID, blocker.ID); err != nil {
		t.Fatalf("adding dependency: %v", err)
	}

	if err := b.DeleteItem(item.ID); err != nil {
		t.Fatalf("deleting item: %v", err)
	}
	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}

	tasks, err := b.ListTasks(item.ID)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "step one" {
		t.Errorf("expected the sub-task restored, got %+v", tasks)
	}

	blockers, err := b.GetBlockers(item.ID)
	if err != nil {
		t.Fatalf("getting blockers: %v", err)
	}
	if len(blockers) != 1 || blockers[0].ID != blocker.ID {
		t.Errorf("expected the dependency restored, got %+v", blockers)
	}
}

// A dependency where the deleted item is the blocker cascades too, so an item
// that other work waits on must come back still blocking it.
func TestUndoRestoresDependenciesPointingAtTheDeletedItem(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	blocker := mustCreateItem(t, b, model.CreateProjectItem{Title: "doomed blocker", ProjectIDs: []string{p.ID}})
	dependent := mustCreateItem(t, b, model.CreateProjectItem{Title: "waiting", ProjectIDs: []string{p.ID}})

	if err := b.AddDependency(dependent.ID, blocker.ID); err != nil {
		t.Fatalf("adding dependency: %v", err)
	}
	if err := b.DeleteItem(blocker.ID); err != nil {
		t.Fatalf("deleting blocker: %v", err)
	}
	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}

	blockers, err := b.GetBlockers(dependent.ID)
	if err != nil {
		t.Fatalf("getting blockers: %v", err)
	}
	if len(blockers) != 1 || blockers[0].ID != blocker.ID {
		t.Errorf("expected the blocking relationship restored, got %+v", blockers)
	}
}

func TestUndoRestoresDeletedProjectMemberships(t *testing.T) {
	b := newTestBackend(t)

	keep := mustCreateProject(t, b, "keep")
	doomed := mustCreateProject(t, b, "doomed")
	item := mustCreateItem(t, b, model.CreateProjectItem{
		Title:      "shared",
		ProjectIDs: []string{keep.ID, doomed.ID},
	})

	if err := b.DeleteProject(doomed.ID); err != nil {
		t.Fatalf("deleting project: %v", err)
	}
	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}

	projects, err := b.GetItemProjects(item.ID)
	if err != nil {
		t.Fatalf("getting item projects: %v", err)
	}
	if len(projects) != 2 {
		t.Errorf("expected the item back in both projects, got %d", len(projects))
	}
}

// Undo preserves identity, not just content: created_at drives item ordering
// and a restore that stamps "now" silently reorders the list.
func TestUndoPreservesDeletedItemTimestamps(t *testing.T) {
	b := newTestBackend(t)

	p := mustCreateProject(t, b, "work")
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "doomed", ProjectIDs: []string{p.ID}})

	before, err := b.GetItem(item.ID)
	if err != nil {
		t.Fatalf("getting item: %v", err)
	}

	if err := b.DeleteItem(item.ID); err != nil {
		t.Fatalf("deleting item: %v", err)
	}
	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}

	after, err := b.GetItem(item.ID)
	if err != nil {
		t.Fatalf("getting restored item: %v", err)
	}
	if after.CreatedAt != before.CreatedAt {
		t.Errorf("created_at changed on restore: %q → %q", before.CreatedAt, after.CreatedAt)
	}
}

// Two running copies reach this on one file, such as the board and a command. Each rule reads and then writes, and SQLite
// refuses that upgrade once another writer has committed unless the transaction
// began with the write lock.
func TestAnEditSurvivesASecondWriter(t *testing.T) {
	var writer func() *Service
	if runsOnPostgres() {
		url := postgrestest.URL(t)
		writer = func() *Service {
			pool, err := postgres.Connect(context.Background(), url)
			if err != nil {
				t.Fatalf("connecting: %v", err)
			}
			t.Cleanup(pool.Close)
			return New(postgres.New(pool))
		}
	} else {
		path := filepath.Join(t.TempDir(), "projects.db")
		writer = func() *Service {
			database, err := sqlite.Open(path)
			if err != nil {
				t.Fatalf("opening database: %v", err)
			}
			t.Cleanup(func() { _ = database.Close() })
			return New(sqlite.New(database))
		}
	}
	first, second := writer(), writer()
	p := mustCreateProject(t, first, "work")
	item := mustCreateItem(t, first, model.CreateProjectItem{Title: "edited", ProjectIDs: []string{p.ID}})

	var edits sync.WaitGroup
	for _, b := range []*Service{first, second} {
		edits.Go(func() {
			for i := range 100 {
				title := fmt.Sprintf("edit %d", i)
				if _, err := b.UpdateItem(item.ID, model.UpdateProjectItem{Title: &title}); err != nil {
					t.Errorf("editing beside another writer: %v", err)
					return
				}
			}
		})
	}
	edits.Wait()
}

// writeUndoEntry puts an entry in the log exactly as an earlier release left it.
func writeUndoEntry(t *testing.T, database *sql.DB, action, entityType, entityID, previousState string) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO undo_log (action, entity_type, entity_id, previous_state) VALUES (?, ?, ?, ?)`,
		action, entityType, entityID, previousState,
	); err != nil {
		t.Fatalf("writing undo entry: %v", err)
	}
}

func newTestBackendWithDatabase(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(sqlite.New(database)), database
}

// Undo logs are not pruned, so entries written before snapshots existed still
// have to restore the row rather than failing or restoring an empty one.
func TestUndoAcceptsPreSnapshotDeleteEntries(t *testing.T) {
	b, database := newTestBackendWithDatabase(t)

	p := mustCreateProject(t, b, "work")
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "legacy", ProjectIDs: []string{p.ID}})

	if err := b.DeleteItem(item.ID); err != nil {
		t.Fatalf("deleting item: %v", err)
	}
	writeUndoEntry(t, database, "delete", "item", item.ID, fmt.Sprintf(
		`{"ID":%q,"Number":{"Int64":0,"Valid":false},"Title":"legacy",`+
			`"Notes":{"String":"","Valid":false},"Repo":{"String":"","Valid":false},`+
			`"Completed":0,"Archived":0,"CreatedAt":"2026-01-02 03:04:05","UpdatedAt":"2026-01-02 03:04:05"}`,
		item.ID))

	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}
	restored, err := b.GetItem(item.ID)
	if err != nil || restored.Title != "legacy" {
		t.Fatalf("expected the item restored from a pre-snapshot entry, got %+v, %v", restored, err)
	}
}

// previous_state is a format on disk that a round trip cannot pin: a field renamed
// in the records still undoes what it wrote itself, and strands what is already in
// the log. These three entries carry every record type, spelled the way it was written.
func TestUndoReadsEntriesAnEarlierReleaseWrote(t *testing.T) {
	t.Run("project row", func(t *testing.T) {
		b, database := newTestBackendWithDatabase(t)
		p := mustCreateProject(t, b, "renamed since")

		writeUndoEntry(t, database, "update", "project", p.ID, fmt.Sprintf(
			`{"ID":%q,"Name":"as it was","Description":{"String":"its text","Valid":true},"Status":"dropped",`+
				`"StatusReason":{"String":"its reason","Valid":true},"ClosedAt":{"String":"2026-01-02T03:04:05.000Z","Valid":true},`+
				`"Position":7,"CreatedAt":"2026-01-01 00:00:00"}`,
			p.ID))
		if _, err := b.Undo(); err != nil {
			t.Fatalf("undoing: %v", err)
		}

		got, err := b.GetProject(p.ID)
		if err != nil {
			t.Fatalf("getting project: %v", err)
		}
		closed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		if got.Name != "as it was" || *got.Description != "its text" || got.Status != model.StatusDropped ||
			*got.StatusReason != "its reason" || !got.ClosedAt.Equal(closed) || got.Position != 7 {
			t.Errorf("restored project = %+v", got)
		}
	})

	t.Run("project snapshot", func(t *testing.T) {
		b, database := newTestBackendWithDatabase(t)

		const projectID = "01900000-0000-7000-8000-000000000001"
		writeUndoEntry(t, database, "delete", "project", projectID, fmt.Sprintf(
			`{"project":{"ID":%q,"Name":"as it was","Description":{"String":"","Valid":false},"Status":"active",`+
				`"StatusReason":{"String":"","Valid":false},"ClosedAt":{"String":"","Valid":false},`+
				`"Position":7,"CreatedAt":"2026-01-01 00:00:00"},"memberships":null}`,
			projectID))
		if _, err := b.Undo(); err != nil {
			t.Fatalf("undoing: %v", err)
		}

		got, err := b.GetProject(projectID)
		if err != nil {
			t.Fatalf("getting project: %v", err)
		}
		if created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); !got.CreatedAt.Equal(created) {
			t.Errorf("restored created_at = %v, want %v", got.CreatedAt, created)
		}
	})

	t.Run("item snapshot", func(t *testing.T) {
		b, database := newTestBackendWithDatabase(t)
		p := mustCreateProject(t, b, "work")
		blocker := mustCreateItem(t, b, model.CreateProjectItem{Title: "blocker", ProjectIDs: []string{p.ID}})

		const itemID, taskID = "01900000-0000-7000-8000-000000000002", "01900000-0000-7000-8000-000000000003"
		writeUndoEntry(t, database, "delete", "item", itemID, fmt.Sprintf(
			`{"item":{"ID":%[1]q,"Number":{"Int64":41,"Valid":true},"Title":"as it was",`+
				`"Notes":{"String":"its notes","Valid":true},"Repo":{"String":"its-repo","Valid":true},`+
				`"Completed":1,"Archived":1,"CreatedAt":"2026-01-02T03:04:05.123Z","UpdatedAt":"2026-01-03T04:05:06.456Z"},`+
				`"memberships":[{"ItemID":%[1]q,"ProjectID":%[2]q,"Position":3}],`+
				`"dependencies":[{"ItemID":%[1]q,"DependsOnID":%[3]q}],`+
				`"tasks":[{"ID":%[4]q,"ItemID":%[1]q,"Title":"its task","Completed":1,"Position":2,"CreatedAt":"2026-01-04T05:06:07.789Z"}]}`,
			itemID, p.ID, blocker.ID, taskID))
		if _, err := b.Undo(); err != nil {
			t.Fatalf("undoing: %v", err)
		}

		got, err := b.GetItem(itemID)
		if err != nil {
			t.Fatalf("getting item: %v", err)
		}
		created := time.Date(2026, 1, 2, 3, 4, 5, 123_000_000, time.UTC)
		updated := time.Date(2026, 1, 3, 4, 5, 6, 456_000_000, time.UTC)
		if *got.Number != 41 || got.Title != "as it was" || *got.Notes != "its notes" || *got.Repo != "its-repo" ||
			!got.Completed || !got.Archived || !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(updated) {
			t.Errorf("restored item = %+v", got)
		}
		if len(got.Projects) != 1 || got.Projects[0].ID != p.ID || len(got.DependencyIDs) != 1 || got.DependencyIDs[0] != blocker.ID {
			t.Errorf("restored links: projects %+v, dependencies %v", got.Projects, got.DependencyIDs)
		}
		memberships, err := b.ListMemberships()
		if err != nil {
			t.Fatalf("listing memberships: %v", err)
		}
		if len(memberships) != 2 || !slices.Contains(memberships, model.Membership{ItemID: itemID, ProjectID: p.ID, Position: 3}) {
			t.Errorf("restored memberships = %+v", memberships)
		}
		tasks, err := b.ListTasks(itemID)
		if err != nil {
			t.Fatalf("listing tasks: %v", err)
		}
		taskCreated := time.Date(2026, 1, 4, 5, 6, 7, 789_000_000, time.UTC)
		if len(tasks) != 1 || tasks[0].ID != taskID || tasks[0].Title != "its task" || !tasks[0].Completed || tasks[0].Position != 2 ||
			!tasks[0].CreatedAt.Equal(taskCreated) {
			t.Errorf("restored tasks = %+v", tasks)
		}
	})
}

// Each mutation below writes to the undo log. A mutation that wrote nothing
// would let undo reach past it and reverse an older, unrelated change.

func TestUndoCoversTaskCreate(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "work")
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "item", ProjectIDs: []string{p.ID}})

	task, err := b.CreateTask(item.ID, model.CreateProjectItemTask{Title: "step"})
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}

	result, err := b.Undo()
	if err != nil {
		t.Fatalf("undoing: %v", err)
	}
	if result.EntityType != "task" || result.EntityID != task.ID {
		t.Fatalf("expected undo of task %s, got %s %s", task.ID, result.EntityType, result.EntityID)
	}

	tasks, err := b.ListTasks(item.ID)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected the task create reversed, got %d tasks", len(tasks))
	}
	if _, err := b.GetItem(item.ID); err != nil {
		t.Errorf("undo reached past the task create and touched the item: %v", err)
	}
}

func TestUndoCoversTaskCompletion(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "work")
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "item", ProjectIDs: []string{p.ID}})
	task, err := b.CreateTask(item.ID, model.CreateProjectItemTask{Title: "step"})
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}

	done := true
	if _, err := b.UpdateTask(item.ID, task.ID, model.UpdateProjectItemTask{Completed: &done}); err != nil {
		t.Fatalf("completing task: %v", err)
	}
	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}

	tasks, err := b.ListTasks(item.ID)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected the task still present, got %d", len(tasks))
	}
	if tasks[0].Completed {
		t.Errorf("expected the completion reversed")
	}
}

func TestUndoCoversTaskDelete(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "work")
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "item", ProjectIDs: []string{p.ID}})
	task, err := b.CreateTask(item.ID, model.CreateProjectItemTask{Title: "step"})
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}

	if err := b.DeleteTask(item.ID, task.ID); err != nil {
		t.Fatalf("deleting task: %v", err)
	}
	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}

	tasks, err := b.ListTasks(item.ID)
	if err != nil {
		t.Fatalf("listing tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "step" {
		t.Errorf("expected the deleted task restored, got %+v", tasks)
	}
}

func TestUndoCoversMembershipAdd(t *testing.T) {
	b := newTestBackend(t)
	work := mustCreateProject(t, b, "work")
	personal := mustCreateProject(t, b, "personal")
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "item", ProjectIDs: []string{work.ID}})

	if err := b.AddToProject(item.ID, personal.ID); err != nil {
		t.Fatalf("adding to project: %v", err)
	}
	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}

	projects, err := b.GetItemProjects(item.ID)
	if err != nil {
		t.Fatalf("getting item projects: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != work.ID {
		t.Errorf("expected only the original membership, got %+v", projects)
	}
}

func TestUndoCoversMembershipRemove(t *testing.T) {
	b := newTestBackend(t)
	work := mustCreateProject(t, b, "work")
	personal := mustCreateProject(t, b, "personal")
	item := mustCreateItem(t, b, model.CreateProjectItem{
		Title:      "item",
		ProjectIDs: []string{work.ID, personal.ID},
	})

	if err := b.RemoveFromProject(item.ID, personal.ID); err != nil {
		t.Fatalf("removing from project: %v", err)
	}
	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}

	projects, err := b.GetItemProjects(item.ID)
	if err != nil {
		t.Fatalf("getting item projects: %v", err)
	}
	if len(projects) != 2 {
		t.Errorf("expected the membership back, got %d", len(projects))
	}
}

func TestUndoCoversDependencyAdd(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "work")
	blocker := mustCreateItem(t, b, model.CreateProjectItem{Title: "blocker", ProjectIDs: []string{p.ID}})
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "blocked", ProjectIDs: []string{p.ID}})

	if err := b.AddDependency(item.ID, blocker.ID); err != nil {
		t.Fatalf("adding dependency: %v", err)
	}
	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}

	blockers, err := b.GetBlockers(item.ID)
	if err != nil {
		t.Fatalf("getting blockers: %v", err)
	}
	if len(blockers) != 0 {
		t.Errorf("expected the dependency reversed, got %+v", blockers)
	}
}

func TestUndoCoversDependencyRemove(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "work")
	blocker := mustCreateItem(t, b, model.CreateProjectItem{Title: "blocker", ProjectIDs: []string{p.ID}})
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "blocked", ProjectIDs: []string{p.ID}})
	if err := b.AddDependency(item.ID, blocker.ID); err != nil {
		t.Fatalf("adding dependency: %v", err)
	}

	if err := b.RemoveDependency(item.ID, blocker.ID); err != nil {
		t.Fatalf("removing dependency: %v", err)
	}
	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}

	blockers, err := b.GetBlockers(item.ID)
	if err != nil {
		t.Fatalf("getting blockers: %v", err)
	}
	if len(blockers) != 1 {
		t.Errorf("expected the dependency back, got %+v", blockers)
	}
}

func TestUndoCoversItemReorder(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "work")
	first := mustCreateItem(t, b, model.CreateProjectItem{Title: "first", ProjectIDs: []string{p.ID}})
	mustCreateItem(t, b, model.CreateProjectItem{Title: "second", ProjectIDs: []string{p.ID}})

	before, err := b.ListItemsByProject(p.ID)
	if err != nil {
		t.Fatalf("listing items: %v", err)
	}

	if err := b.ReorderItem(first.ID, p.ID, 99); err != nil {
		t.Fatalf("reordering item: %v", err)
	}
	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}

	after, err := b.ListItemsByProject(p.ID)
	if err != nil {
		t.Fatalf("listing items: %v", err)
	}
	if len(after) != len(before) || after[0].ID != before[0].ID {
		t.Errorf("expected the original order back, got %+v", after)
	}
}

func TestUndoCoversProjectReorder(t *testing.T) {
	b := newTestBackend(t)
	first := mustCreateProject(t, b, "first")
	mustCreateProject(t, b, "second")

	before, err := b.GetProject(first.ID)
	if err != nil {
		t.Fatalf("getting project: %v", err)
	}

	if err := b.ReorderProject(first.ID, 99); err != nil {
		t.Fatalf("reordering project: %v", err)
	}
	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}

	after, err := b.GetProject(first.ID)
	if err != nil {
		t.Fatalf("getting project: %v", err)
	}
	if after.Position != before.Position {
		t.Errorf("expected position %d back, got %d", before.Position, after.Position)
	}
}

// The whole point of covering these mutations: an undo must reverse the last
// thing that happened, never skip it and reach an older entry.
func TestUndoDoesNotSkipPastUnloggedMutations(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "work")
	item := mustCreateItem(t, b, model.CreateProjectItem{Title: "original", ProjectIDs: []string{p.ID}})

	renamed := "renamed"
	if _, err := b.UpdateItem(item.ID, model.UpdateProjectItem{Title: &renamed}); err != nil {
		t.Fatalf("renaming item: %v", err)
	}
	if _, err := b.CreateTask(item.ID, model.CreateProjectItemTask{Title: "step"}); err != nil {
		t.Fatalf("creating task: %v", err)
	}

	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing: %v", err)
	}

	current, err := b.GetItem(item.ID)
	if err != nil {
		t.Fatalf("getting item: %v", err)
	}
	if current.Title != "renamed" {
		t.Errorf("undo skipped the task create and reversed the rename: title is %q", current.Title)
	}
}
