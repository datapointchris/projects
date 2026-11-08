package service_test

import (
	"database/sql"
	"testing"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/service"
	"github.com/datapointchris/projects/store/sqlite"
)

func newNumberTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// An item's number is the handle a person types, so a fresh database starts it at 1.
func TestCreateItem_NumbersAFreshDatabaseFromOne(t *testing.T) {
	b := service.New(sqlite.New(newNumberTestDB(t)))
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
			t.Fatalf("CreateItem(%s) left the number unset", title)
		}
		numbers = append(numbers, *item.Number)
	}

	if want := []int{1, 2, 3}; numbers[0] != want[0] || numbers[1] != want[1] || numbers[2] != want[2] {
		t.Errorf("numbers = %v, want %v — the first item in a fresh database is 1", numbers, want)
	}
}
