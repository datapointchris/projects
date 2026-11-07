// Package postgres is store.Store over Postgres.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // goose reads through database/sql
	"github.com/pressly/goose/v3"
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
