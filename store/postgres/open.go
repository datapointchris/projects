// Package postgres is store.Store over Postgres.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // goose reads through database/sql
	"github.com/pressly/goose/v3"

	"github.com/datapointchris/projects/model"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrate brings the database at url up to the newest migration. It runs from a
// job before the servers roll, so two servers starting together cannot race it and
// a bad migration fails the job rather than crash-looping what is serving.
func Migrate(ctx context.Context, url string) error {
	database, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("opening database for migration: %w", err)
	}
	defer func() { _ = database.Close() }()
	if err := waitForServer(ctx, database); err != nil {
		return err
	}

	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("reading the embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, database, files)
	if err != nil {
		return fmt.Errorf("preparing migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	return upsertLookupRows(ctx, database)
}

// serverWait is how long a migration waits for its first connection. A cluster's
// network policy can refuse a new pod's connections for a moment after it starts,
// and the job's pod connects once, so without the wait every attempt fails.
const serverWait = 60 * time.Second

// waitForServer pings until the server answers. Only a refused connection is
// waited on, so a wrong password or database still fails at once.
func waitForServer(ctx context.Context, database *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, serverWait)
	defer cancel()
	for {
		err := database.PingContext(ctx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			return fmt.Errorf("connecting for migration: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the database refused every connection for %s: %w", serverWait, err)
		case <-time.After(2 * time.Second):
		}
	}
}

// upsertLookupRows runs on every migrate, so a kind or status added to model reaches
// a database that has already taken every migration.
func upsertLookupRows(ctx context.Context, database *sql.DB) error {
	lookups := []struct {
		statement string
		names     []string
	}{
		{"INSERT INTO project_kinds (name) VALUES ($1) ON CONFLICT (name) DO NOTHING", model.ProjectKinds},
		{"INSERT INTO project_statuses (name) VALUES ($1) ON CONFLICT (name) DO NOTHING", model.ProjectStatuses},
	}
	for _, lookup := range lookups {
		for _, name := range lookup.names {
			if _, err := database.ExecContext(ctx, lookup.statement, name); err != nil {
				return fmt.Errorf("adding lookup row %q: %w", name, err)
			}
		}
	}
	return nil
}

// Connect opens a connection pool the caller closes.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("opening connection pool: %w", err)
	}
	return pool, nil
}
