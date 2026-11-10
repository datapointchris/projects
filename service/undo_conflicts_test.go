package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store"
)

// One person's two machines write through one API. Each undo reaches only its own
// machine's writes, and one whose reversal would revert the other machine's later
// write is refused with that write named, then dropped.
func TestAnUndoNeverRevertsAnotherWritersWrite(t *testing.T) {
	b := newTestBackend(t)
	as := func(client string) *Service {
		return b.WithContext(store.WithWriter(context.Background(), store.Writer{Actor: "a-person", Client: client}))
	}
	desk, laptop := as("projects-cli-desk"), as("projects-cli-laptop")
	text := func(s string) *string { return &s }
	refusedNaming := func(t *testing.T, err error, words ...string) {
		t.Helper()
		if !errors.Is(err, model.ErrUndoSuperseded) {
			t.Fatalf("the desk's undo answered %v, want a refusal", err)
		}
		t.Log(err)
		for _, word := range words {
			if !strings.Contains(err.Error(), word) {
				t.Errorf("the refusal %q does not name %q", err, word)
			}
		}
	}

	project := mustCreateProject(t, desk, "work")
	item := mustCreateItem(t, desk, model.CreateProjectItem{Title: "before", ProjectIDs: []string{project.ID}})
	if canUndo, _ := laptop.CanUndo(); canUndo {
		t.Error("the laptop has written nothing, yet it can undo")
	}

	if _, err := desk.UpdateItem(item.ID, model.UpdateProjectItem{Title: text("desk's title")}); err != nil {
		t.Fatal(err)
	}
	if _, err := laptop.UpdateItem(item.ID, model.UpdateProjectItem{Notes: text("laptop's note")}); err != nil {
		t.Fatal(err)
	}
	_, err := desk.Undo()
	refusedNaming(t, err, "projects-cli-laptop", "update on item")
	got, err := b.GetItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "desk's title" || got.Notes == nil || *got.Notes != "laptop's note" {
		t.Errorf("after the refusal the item reads %q with notes %v, want both edits standing", got.Title, got.Notes)
	}

	// The refused entry is gone, so the next undo reaches the item's create. The
	// laptop's edit to that item refuses it too.
	_, err = desk.Undo()
	refusedNaming(t, err, "create on item")

	// Undoing the project's create would leave both items in no project.
	laptopItem := mustCreateItem(t, laptop, model.CreateProjectItem{Title: "laptop's item", ProjectIDs: []string{project.ID}})
	_, err = desk.Undo()
	refusedNaming(t, err, "create on project", "no project")
	if projects, err := b.GetItemProjects(laptopItem.ID); err != nil || len(projects) != 1 {
		t.Errorf("the laptop's item is in %v (%v), want the project it was made in", projects, err)
	}
	if canUndo, _ := desk.CanUndo(); canUndo {
		t.Error("every desk entry was refused and dropped, yet the desk can undo")
	}

	if _, err := laptop.Undo(); err != nil {
		t.Fatalf("the laptop's own undo answered %v", err)
	}
	if _, err := b.GetItem(laptopItem.ID); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("the laptop's undo left its create standing: %v", err)
	}
}
