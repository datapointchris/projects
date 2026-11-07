package backend_test

import (
	"database/sql"
	"testing"

	"github.com/datapointchris/projects/backend"
	"github.com/datapointchris/projects/db"
	"github.com/datapointchris/projects/model"
)

func newNumberTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// The work machine is never allowed to reach the API, so nothing upstream will
// ever number its items. Its database is the authority instead.
func TestCreateItem_AssignsNumbersWhenThisDatabaseIsTheAuthority(t *testing.T) {
	b := backend.NewLocalBackend(newNumberTestDB(t), backend.AssigningItemNumbers())
	project, err := b.CreateProject(model.CreateProject{Name: "Work"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	var numbers []int
	for _, title := range []string{"First", "Second", "Third"} {
		item, err := b.CreateItem(model.CreateProjectItem{Title: title, ProjectIDs: []string{project.ID}})
		if err != nil {
			t.Fatalf("CreateItem(%s): %v", title, err)
		}
		if item.Number == nil {
			t.Fatalf("CreateItem(%s) left the number unset — nothing else will assign it here", title)
		}
		numbers = append(numbers, *item.Number)
	}

	if want := []int{1, 2, 3}; numbers[0] != want[0] || numbers[1] != want[1] || numbers[2] != want[2] {
		t.Errorf("numbers = %v, want %v — the first item in a fresh database is 1", numbers, want)
	}
}

// With sync on the server owns the numbering. Guessing one locally would hand
// the user a handle that changes the moment the push comes back.
func TestCreateItem_LeavesTheNumberToTheServerWhenSyncIsOn(t *testing.T) {
	b := backend.NewLocalBackend(newNumberTestDB(t))
	project, err := b.CreateProject(model.CreateProject{Name: "Synced"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	item, err := b.CreateItem(model.CreateProjectItem{Title: "Waits for a number", ProjectIDs: []string{project.ID}})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	if item.Number != nil {
		t.Errorf("Number = %d, want nil — the server assigns it and the two would disagree", *item.Number)
	}
}
