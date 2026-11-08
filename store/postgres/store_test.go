package postgres_test

import (
	"context"
	"slices"
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
	ctx := store.WithWriter(context.Background(), store.Writer{
		Actor: "a-person", RequestID: "a-request", JobRunID: "a-run", Traceparent: "a-trace", Baggage: "a-baggage",
	})
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
		SELECT table_name, operation, actor, request_id, job_run_id, traceparent, baggage
		FROM audit_trail
		WHERE table_name <> 'project_kinds' AND table_name <> 'project_statuses'
		ORDER BY id`)
	if err != nil {
		t.Fatalf("reading the trail: %v", err)
	}
	defer rows.Close()

	seen := map[string]bool{}
	for rows.Next() {
		var table, operation, actor, requestID, jobRunID, traceparent, baggage string
		if err := rows.Scan(&table, &operation, &actor, &requestID, &jobRunID, &traceparent, &baggage); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		if actor != "a-person" || requestID != "a-request" || jobRunID != "a-run" || traceparent != "a-trace" || baggage != "a-baggage" {
			t.Errorf("%s %s recorded writer %q, request %q, run %q, trace %q, baggage %q",
				operation, table, actor, requestID, jobRunID, traceparent, baggage)
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
}

// A table added to the migrations has to bring its trigger, or its writes go
// unrecorded with nothing to say so.
func TestEveryTableIsAuditedButWhatOptsOut(t *testing.T) {
	pool := postgrestest.Pool(t)
	optsOut := []string{"audit_trail", "browser_login_attempts", "browser_sessions", "goose_db_version", "undo_log"}

	rows, err := pool.Query(context.Background(), `
		SELECT c.relname, EXISTS (
			SELECT 1 FROM pg_trigger AS tr WHERE tr.tgrelid = c.oid AND tr.tgname = 'audit_trail'
		)
		FROM pg_class AS c
		JOIN pg_namespace AS n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r' AND n.nspname = 'public'`)
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var table string
		var audited bool
		if err := rows.Scan(&table, &audited); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		if audited == slices.Contains(optsOut, table) {
			t.Errorf("table %s: audited %v, and listed as opting out %v", table, audited, slices.Contains(optsOut, table))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing tables: %v", err)
	}
}
