package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/store/sqlite"
)

// The next column on a project, item or task reaches this. A field its record or
// its restore leaves out comes back empty after a delete and undo, and nothing
// reports it.
func TestEveryModelFieldSurvivesAnUndo(t *testing.T) {
	ctx := context.Background()
	storeForTest := func(t *testing.T) *sqlite.Store {
		t.Helper()
		database, err := sqlite.Open(":memory:")
		if err != nil {
			t.Fatalf("opening test db: %v", err)
		}
		t.Cleanup(func() { _ = database.Close() })
		return sqlite.New(database)
	}

	t.Run("project", func(t *testing.T) {
		var want model.Project
		fillEveryField(t, &want)
		assertSameFields(t, "its record", projectRecordOf(want).toModel(), want)

		st := storeForTest(t)
		if err := st.RestoreProject(ctx, want); err != nil {
			t.Fatalf("restoring: %v", err)
		}
		got, err := st.GetProject(ctx, want.ID)
		if err != nil {
			t.Fatalf("reading back: %v", err)
		}
		assertSameFields(t, "its restore", got, want)
	})

	t.Run("item", func(t *testing.T) {
		var want model.ProjectItem
		fillEveryField(t, &want)
		assertSameFields(t, "its record", itemRecordOf(want).toModel(), want)

		st := storeForTest(t)
		if err := st.RestoreItem(ctx, want); err != nil {
			t.Fatalf("restoring: %v", err)
		}
		got, err := st.GetItem(ctx, want.ID)
		if err != nil {
			t.Fatalf("reading back: %v", err)
		}
		assertSameFields(t, "its restore", got, want)
	})

	t.Run("task", func(t *testing.T) {
		var want model.ProjectItemTask
		fillEveryField(t, &want)
		assertSameFields(t, "its record", taskRecordOf(want).toModel(), want)

		st := storeForTest(t)
		if err := st.RestoreItem(ctx, model.ProjectItem{ID: want.ItemID, Title: "its item"}); err != nil {
			t.Fatalf("restoring its item: %v", err)
		}
		if err := st.RestoreTask(ctx, want); err != nil {
			t.Fatalf("restoring: %v", err)
		}
		got, err := st.GetTask(ctx, want.ID)
		if err != nil {
			t.Fatalf("reading back: %v", err)
		}
		assertSameFields(t, "its restore", got, want)
	})
}

// fillEveryField gives each field a value other than its zero. A field of a type
// it has no value for fails, so a new kind of column cannot pass by staying empty.
func fillEveryField(t *testing.T, target any) {
	t.Helper()
	at := time.Date(2026, 1, 2, 3, 4, 5, 123_000_000, time.UTC)
	text, number := "text", 7

	fields := reflect.ValueOf(target).Elem()
	for i := range fields.NumField() {
		switch field := fields.Field(i).Addr().Interface().(type) {
		case *string:
			*field = text
		case **string:
			*field = &text
		case *int:
			*field = number
		case **int:
			*field = &number
		case *bool:
			*field = true
		case *time.Time:
			*field = at
		case **time.Time:
			*field = &at
		default:
			t.Fatalf("no value for %s.%s of type %s", fields.Type(), fields.Type().Field(i).Name, fields.Field(i).Type())
		}
	}
}

func assertSameFields[M any](t *testing.T, through string, got, want M) {
	t.Helper()
	gotFields, wantFields := reflect.ValueOf(got), reflect.ValueOf(want)
	for i := range wantFields.NumField() {
		if !reflect.DeepEqual(gotFields.Field(i).Interface(), wantFields.Field(i).Interface()) {
			t.Errorf("%s through %s = %v, want %v", wantFields.Type().Field(i).Name, through,
				reflect.Indirect(gotFields.Field(i)), reflect.Indirect(wantFields.Field(i)))
		}
	}
}
