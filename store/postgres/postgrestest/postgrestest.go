// Package postgrestest gives a test a migrated database of its own. One container
// serves the whole test binary, or the server TEST_DATABASE_URL names, and each test
// clones a migrated template, which is cheaper than migrating.
package postgrestest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/datapointchris/projects/store/postgres"
)

const templateName = "projects_test_template"

var (
	prepareServer sync.Once
	adminURL      string
	errPrepare    error
	clones        atomic.Int64
)

// URL answers the connection string of an empty migrated database the test owns. A
// short run skips, because the commit gate runs short and this needs a server.
func URL(t testing.TB) string {
	t.Helper()
	if testing.Short() {
		t.Skip("needs a Postgres server, which a short run does not start")
	}
	prepareServer.Do(func() { adminURL, errPrepare = startServerWithTemplate(context.Background()) })
	if errPrepare != nil {
		t.Fatalf("preparing Postgres: %v", errPrepare)
	}

	ctx := context.Background()
	name := fmt.Sprintf("projects_test_%d_%d", os.Getpid(), clones.Add(1))
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connecting to Postgres: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, templateName)); err != nil {
		t.Fatalf("cloning the template: %v", err)
	}
	return withDatabase(adminURL, name)
}

// Pool answers a pool over a database URL made, closed when the test ends.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, err := postgres.Connect(context.Background(), URL(t))
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func startServerWithTemplate(ctx context.Context) (string, error) {
	server := os.Getenv("TEST_DATABASE_URL")
	if server == "" {
		container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("projects_test"),
			tcpostgres.WithUsername("projects"),
			tcpostgres.WithPassword("projects"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(60*time.Second),
			),
		)
		if err != nil {
			return "", fmt.Errorf("starting a container: %w", err)
		}
		server, err = container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			return "", fmt.Errorf("reading the container's address: %w", err)
		}
	}

	admin, err := pgx.Connect(ctx, server)
	if err != nil {
		return "", fmt.Errorf("connecting: %w", err)
	}
	defer func() { _ = admin.Close(ctx) }()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+templateName); err != nil {
		return "", fmt.Errorf("dropping an old template: %w", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+templateName); err != nil {
		return "", fmt.Errorf("creating the template: %w", err)
	}
	if err := postgres.Migrate(ctx, withDatabase(server, templateName)); err != nil {
		return "", fmt.Errorf("migrating the template: %w", err)
	}
	return server, nil
}

func withDatabase(server, name string) string {
	parsed, err := url.Parse(server)
	if err != nil {
		return server
	}
	parsed.Path = "/" + strings.TrimPrefix(name, "/")
	return parsed.String()
}
