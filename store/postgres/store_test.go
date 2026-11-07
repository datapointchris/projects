package postgres_test

import (
	"context"
	"testing"

	"github.com/datapointchris/projects/model"
	"github.com/datapointchris/projects/service"
	"github.com/datapointchris/projects/store"
	"github.com/datapointchris/projects/store/postgres"
	"github.com/datapointchris/projects/store/postgres/postgrestest"
)

// A write's audit row names who wrote it, which only the transaction-local settings
// InTx binds can carry to the trigger, and a cascade is recorded though no statement
// named its rows.
func TestEveryWriteIsAuditedWithItsWriter(t *testing.T) {
	pool := postgrestest.Pool(t)
	ctx := store.WithWriter(context.Background(), store.Writer{Actor: "a-person", RequestID: "a-request", JobRunID: "a-run"})
	svc := service.New(postgres.New(pool), service.AssigningItemNumbers()).WithContext(ctx)

	project, err := svc.CreateProject(model.CreateProject{Name: "audited"})
	if err != nil {
		t.Fatalf("creating project: %v", err)
	}
	item, err := svc.CreateItem(model.CreateProjectItem{Title: "an item", ProjectIDs: []string{project.ID}})
	if err != nil {
		t.Fatalf("creating item: %v", err)
	}
	if err := svc.DeleteItem(item.ID); err != nil {
		t.Fatalf("deleting item: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT table_name, operation, actor, request_id, job_run_id
		FROM audit_trail
		WHERE table_name <> 'project_kinds' AND table_name <> 'project_statuses'
		ORDER BY id`)
	if err != nil {
		t.Fatalf("reading the trail: %v", err)
	}
	defer rows.Close()

	seen := map[string]bool{}
	for rows.Next() {
		var table, operation, actor, requestID, jobRunID string
		if err := rows.Scan(&table, &operation, &actor, &requestID, &jobRunID); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		if actor != "a-person" || requestID != "a-request" || jobRunID != "a-run" {
			t.Errorf("%s %s recorded writer %q, request %q, run %q", operation, table, actor, requestID, jobRunID)
		}
		seen[table+" "+operation] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the trail: %v", err)
	}
	for _, want := range []string{
		"projects insert",
		"project_items insert",
		"project_item_memberships insert",
		"project_items delete",
		"project_item_memberships delete",
	} {
		if !seen[want] {
			t.Errorf("the trail holds no %q row: %v", want, seen)
		}
	}
	if seen["undo_log insert"] {
		t.Error("undo_log is audited, which copies each write's whole row twice")
	}
}
