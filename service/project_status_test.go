package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store/sqlite"
)

func mustSetStatus(t *testing.T, b *Service, id, status string, reason *string) *model.Project {
	t.Helper()
	p, err := b.SetProjectStatus(id, status, reason)
	if err != nil {
		t.Fatalf("setting project status to %s: %v", status, err)
	}
	return p
}

func projectNames(projects []model.ProjectWithItemCount) []string {
	names := make([]string, len(projects))
	for i, p := range projects {
		names[i] = p.Name
	}
	return names
}

func TestNewProjectIsActive(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "live")

	if p.Status != model.StatusActive {
		t.Errorf("status = %q, want active", p.Status)
	}
	if p.ClosedAt != nil {
		t.Errorf("closed_at = %v, want nil — the project has not been closed", p.ClosedAt)
	}
}

func TestCompletingHidesTheProject(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "finite effort")

	mustSetStatus(t, b, p.ID, model.StatusCompleted, nil)

	active, err := b.ListProjectsByStatus(model.StatusActive)
	if err != nil {
		t.Fatalf("listing projects: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("completed project still in the active list: %v", projectNames(active))
	}

	all, err := b.ListProjectsByStatus(model.StatusAll)
	if err != nil {
		t.Fatalf("listing all projects: %v", err)
	}
	if len(all) != 1 || all[0].Status != model.StatusCompleted {
		t.Errorf("--status all must still show it, got %v", all)
	}
}

func TestCompletingStampsTheClosingDate(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "finite effort")

	done := mustSetStatus(t, b, p.ID, model.StatusCompleted, nil)

	if done.ClosedAt == nil {
		t.Error("closed_at must be stamped: created_at orders by when work started, not when it ended")
	}
}

func TestDroppingWithoutAReasonIsRefused(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "abandoned")

	for _, reason := range []*string{nil, ptr(""), ptr("   ")} {
		if _, err := b.SetProjectStatus(p.ID, model.StatusDropped, reason); !errors.Is(err, model.ErrDropReasonRequired) {
			t.Errorf("reason %v: got %v, want ErrDropReasonRequired", reason, err)
		}
	}

	current, err := b.GetProject(p.ID)
	if err != nil {
		t.Fatalf("getting project: %v", err)
	}
	if current.Status != model.StatusActive {
		t.Errorf("a refused drop must leave the project alone, got %q", current.Status)
	}
}

func TestDroppingStoresTheReason(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "abandoned")

	dropped := mustSetStatus(t, b, p.ID, model.StatusDropped, ptr("Go covers it"))

	if dropped.Status != model.StatusDropped {
		t.Errorf("status = %q, want dropped", dropped.Status)
	}
	if dropped.StatusReason == nil || *dropped.StatusReason != "Go covers it" {
		t.Errorf("status_reason = %v, want the reason it was dropped", dropped.StatusReason)
	}
	if dropped.ClosedAt == nil {
		t.Error("closed_at must be stamped on a drop as much as on a completion")
	}
}

func TestReopeningClearsTheClosure(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "revived")
	mustSetStatus(t, b, p.ID, model.StatusDropped, ptr("Not worth it"))

	reopened := mustSetStatus(t, b, p.ID, model.StatusActive, nil)

	if reopened.Status != model.StatusActive {
		t.Errorf("status = %q, want active", reopened.Status)
	}
	if reopened.StatusReason != nil {
		t.Errorf("a live project carries no reason for having been closed, got %q", *reopened.StatusReason)
	}
	if reopened.ClosedAt != nil {
		t.Errorf("closed_at = %v, want nil", reopened.ClosedAt)
	}
}

func TestEditingTheReasonKeepsTheOriginalClosingDate(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "abandoned")
	first := mustSetStatus(t, b, p.ID, model.StatusDropped, ptr("first take"))

	second := mustSetStatus(t, b, p.ID, model.StatusDropped, ptr("better wording"))

	if second.ClosedAt == nil || !second.ClosedAt.Equal(*first.ClosedAt) {
		t.Errorf("closed_at = %v, want the original %v — rewording is not a new closing decision",
			second.ClosedAt, first.ClosedAt)
	}
}

func TestClosingAProjectLeavesItsItemsAlone(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "half finished")
	open := mustCreateItem(t, b, model.CreateProjectItem{Title: "never started", ProjectIDs: []string{p.ID}})
	shipped := mustCreateItem(t, b, model.CreateProjectItem{Title: "shipped", ProjectIDs: []string{p.ID}})
	completed := true
	if _, err := b.UpdateItem(shipped.ID, model.UpdateProjectItem{Completed: &completed}); err != nil {
		t.Fatalf("completing item: %v", err)
	}

	mustSetStatus(t, b, p.ID, model.StatusDropped, ptr("Ran out of road"))

	items, err := b.ListItemsByProject(p.ID)
	if err != nil {
		t.Fatalf("listing items: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want both still there", len(items))
	}
	for _, item := range items {
		if item.Archived {
			t.Errorf("item %q was archived by closing the project — that rewrites what happened", item.Title)
		}
		if item.ID == open.ID && item.Completed {
			t.Error("an item still open when the project closed WAS still open")
		}
	}
}

func TestAFinishedProjectGivesItsNameBack(t *testing.T) {
	b := newTestBackend(t)
	first := mustCreateProject(t, b, "clisteno")
	mustSetStatus(t, b, first.ID, model.StatusCompleted, nil)

	second, err := b.CreateProject(model.CreateProject{Name: "clisteno"})
	if err != nil {
		t.Fatalf("a closed project must stop owning its name: %v", err)
	}
	if second.ID == first.ID {
		t.Error("expected a new project, got the old one back")
	}
}

func TestTwoActiveProjectsCannotShareAName(t *testing.T) {
	b := newTestBackend(t)
	mustCreateProject(t, b, "clisteno")

	if _, err := b.CreateProject(model.CreateProject{Name: "clisteno"}); !errors.Is(err, model.ErrDuplicateName) {
		t.Errorf("got %v, want ErrDuplicateName", err)
	}
}

func TestReopeningIntoATakenNameIsRefused(t *testing.T) {
	b := newTestBackend(t)
	closed := mustCreateProject(t, b, "clisteno")
	mustSetStatus(t, b, closed.ID, model.StatusCompleted, nil)
	mustCreateProject(t, b, "clisteno")

	if _, err := b.SetProjectStatus(closed.ID, model.StatusActive, nil); !errors.Is(err, model.ErrDuplicateName) {
		t.Errorf("got %v, want ErrDuplicateName — only the live project holds a name", err)
	}
}

func TestUndoRestoresTheStatusAProjectHad(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "reconsidered")
	mustSetStatus(t, b, p.ID, model.StatusDropped, ptr("thought better of it"))

	if _, err := b.Undo(); err != nil {
		t.Fatalf("undoing the drop: %v", err)
	}

	restored, err := b.GetProject(p.ID)
	if err != nil {
		t.Fatalf("getting project: %v", err)
	}
	if restored.Status != model.StatusActive {
		t.Errorf("status = %q, want active — undo has to reverse the whole row, not just its name", restored.Status)
	}
	if restored.ClosedAt != nil || restored.StatusReason != nil {
		t.Errorf("the closure survived the undo: closed_at=%v reason=%v", restored.ClosedAt, restored.StatusReason)
	}
}

func TestShelvingHidesTheProjectWithoutClosingIt(t *testing.T) {
	b := newTestBackend(t)
	p := mustCreateProject(t, b, "slow burn")

	shelved := mustSetStatus(t, b, p.ID, model.StatusSomeday, ptr("waiting on the API"))

	if shelved.ClosedAt != nil {
		t.Errorf("closed_at = %v, want nil — a shelved project has not ended", shelved.ClosedAt)
	}
	if shelved.StatusReason == nil || *shelved.StatusReason != "waiting on the API" {
		t.Errorf("status_reason = %v, want the reason given", shelved.StatusReason)
	}
	active, err := b.ListProjectsByStatus(model.StatusActive)
	if err != nil {
		t.Fatalf("listing projects: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("shelved project still in the active list: %v", projectNames(active))
	}
}

func TestListGroupsActiveThenSomedayThenClosed(t *testing.T) {
	b := newTestBackend(t)
	closed := mustCreateProject(t, b, "finished")
	shelved := mustCreateProject(t, b, "shelved")
	mustCreateProject(t, b, "live")
	mustSetStatus(t, b, closed.ID, model.StatusCompleted, nil)
	mustSetStatus(t, b, shelved.ID, model.StatusSomeday, nil)

	all, err := b.ListProjectsByStatus(model.StatusAll)
	if err != nil {
		t.Fatalf("listing all projects: %v", err)
	}

	want := []string{"live", "shelved", "finished"}
	if got := projectNames(all); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v regardless of creation order", got, want)
	}
}

func ptr(s string) *string { return &s }

// --- The repo-named-project ban ---

func backendRefusingRepos(t *testing.T, names ...string) *Service {
	t.Helper()
	entries := make([]string, len(names))
	for i, name := range names {
		entries[i] = `{"name":"` + name + `"}`
	}
	path := filepath.Join(t.TempDir(), "repos.json")
	if err := os.WriteFile(path, []byte(`{"repos":[`+strings.Join(entries, ",")+`]}`), 0o600); err != nil {
		t.Fatalf("writing registry: %v", err)
	}
	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(database, RefusingRepoNames(path))
}

func TestAProjectCannotBeNamedAfterARepo(t *testing.T) {
	b := backendRefusingRepos(t, "projects", "dotfiles")

	for _, name := range []string{"projects", "Dotfiles"} {
		_, err := b.CreateProject(model.CreateProject{Name: name})
		if !errors.Is(err, model.ErrRepoNamedProject) {
			t.Errorf("creating %q: got %v, want ErrRepoNamedProject", name, err)
		}
	}
}

func TestBoundedWorkNamingARepoIsAllowed(t *testing.T) {
	// The test is whether the thing ENDS, not whether the repo name appears.
	b := backendRefusingRepos(t, "projects", "dotfiles")

	for _, name := range []string{"projects sync improvements", "Extract xx from dotfiles"} {
		if _, err := b.CreateProject(model.CreateProject{Name: name}); err != nil {
			t.Errorf("creating %q: %v", name, err)
		}
	}
}

// A ban one `edit` walks around is decoration.
func TestAProjectCannotBeRenamedToARepo(t *testing.T) {
	b := backendRefusingRepos(t, "projects")
	p, err := b.CreateProject(model.CreateProject{Name: "projects sync improvements"})
	if err != nil {
		t.Fatalf("creating project: %v", err)
	}

	name := "projects"
	_, err = b.UpdateProject(p.ID, model.UpdateProject{Name: &name})

	if !errors.Is(err, model.ErrRepoNamedProject) {
		t.Errorf("got %v, want ErrRepoNamedProject", err)
	}
}

// Refusing to create a project on a machine without a registry is worse than a
// project named after a repo.
func TestWithoutARegistryNothingIsBanned(t *testing.T) {
	b := newTestBackend(t)

	if _, err := b.CreateProject(model.CreateProject{Name: "projects"}); err != nil {
		t.Errorf("a backend with no registry must ban nothing: %v", err)
	}
}
