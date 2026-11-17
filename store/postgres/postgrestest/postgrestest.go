// Package postgrestest gives a test a migrated database of its own. One container
// serves the whole test binary, or the server TEST_DATABASE_URL names, and each test
// clones a migrated template, which is cheaper than migrating.
//
// The server's login, a superuser, only creates the roles and the databases. The
// template is migrated by its owner, and each test logs in as the app role, as the
// API does, so a grant the cluster would not give fails a test.
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

// A database's roles are named for it, and the migrations narrow the app role by
// that name. So the template's roles carry its name, and each clone copies the
// grants the template's migrations left.
const (
	ownerRole     = templateName
	appRole       = templateName + "_app"
	ownerPassword = "projects-owner"
	appPassword   = "projects-app"
)

// createRoles creates or updates the owner and app roles, their passwords bound as
// settings so no value is quoted into the statement.
const createRoles = `
DO $$
DECLARE
	owner_role text := current_setting('postgrestest.owner_role');
	app_role text := current_setting('postgrestest.app_role');
BEGIN
	IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = owner_role) THEN
		EXECUTE format('CREATE ROLE %I', owner_role);
	END IF;
	IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = app_role) THEN
		EXECUTE format('CREATE ROLE %I', app_role);
	END IF;
	EXECUTE format('ALTER ROLE %I LOGIN PASSWORD %L', owner_role, current_setting('postgrestest.owner_password'));
	EXECUTE format('ALTER ROLE %I LOGIN PASSWORD %L', app_role, current_setting('postgrestest.app_password'));
END;
$$`

// What homelab's reconcile gives a declared database's app role, on an empty database.
var appRoleGrants = []string{
	"REVOKE ALL ON DATABASE %[1]s FROM PUBLIC",
	"GRANT CONNECT ON DATABASE %[1]s TO %[3]s",
	"GRANT USAGE ON SCHEMA public TO %[3]s",
	"REVOKE CREATE ON SCHEMA public FROM PUBLIC, %[3]s",
	"ALTER DEFAULT PRIVILEGES FOR ROLE %[2]s GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %[3]s",
	"ALTER DEFAULT PRIVILEGES FOR ROLE %[2]s GRANT USAGE ON SEQUENCES TO %[3]s",
	"ALTER DEFAULT PRIVILEGES FOR ROLE %[2]s GRANT USAGE ON SCHEMAS TO %[3]s",
}

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
	clone, owner, app := pgx.Identifier{name}.Sanitize(), pgx.Identifier{ownerRole}.Sanitize(), pgx.Identifier{appRole}.Sanitize()
	for _, statement := range []string{
		fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s OWNER %s", clone, pgx.Identifier{templateName}.Sanitize(), owner),
		fmt.Sprintf("REVOKE ALL ON DATABASE %s FROM PUBLIC", clone),
		fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO %s", clone, app),
	} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("cloning the template: %v", err)
		}
	}
	return withLogin(withDatabase(adminURL, name), appRole, appPassword)
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

// StoppableServer answers a migrated database on a container of the test's own, and
// a stop that shuts the server down. Docker stops it with the image's SIGINT, which
// is Postgres's fast shutdown, so every query in flight is cut off.
func StoppableServer(t testing.TB) (url string, stop func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("needs a Postgres server, which a short run does not start")
	}
	ctx := context.Background()
	container, url, err := startContainer(ctx)
	if container != nil {
		t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	}
	if err != nil {
		t.Fatalf("starting Postgres: %v", err)
	}
	if err := postgres.Migrate(ctx, url); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	return url, func() {
		if err := container.Stop(context.Background(), nil); err != nil {
			t.Errorf("stopping Postgres: %v", err)
		}
	}
}

func startContainer(ctx context.Context) (*tcpostgres.PostgresContainer, string, error) {
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("projects_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return container, "", fmt.Errorf("starting a container: %w", err)
	}
	server, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return container, "", fmt.Errorf("reading the container's address: %w", err)
	}
	return container, server, nil
}

func startServerWithTemplate(ctx context.Context) (string, error) {
	server := os.Getenv("TEST_DATABASE_URL")
	if server == "" {
		var err error
		if _, server, err = startContainer(ctx); err != nil {
			return "", err
		}
	}

	admin, err := pgx.Connect(ctx, server)
	if err != nil {
		return "", fmt.Errorf("connecting: %w", err)
	}
	defer func() { _ = admin.Close(ctx) }()
	template, owner, app := pgx.Identifier{templateName}.Sanitize(), pgx.Identifier{ownerRole}.Sanitize(), pgx.Identifier{appRole}.Sanitize()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+template); err != nil {
		return "", fmt.Errorf("dropping an old template: %w", err)
	}
	if err := createRolesAs(ctx, admin); err != nil {
		return "", err
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s", template, owner)); err != nil {
		return "", fmt.Errorf("creating the template: %w", err)
	}

	inTemplate, err := pgx.Connect(ctx, withDatabase(server, templateName))
	if err != nil {
		return "", fmt.Errorf("connecting to the template: %w", err)
	}
	defer func() { _ = inTemplate.Close(ctx) }()
	for _, grant := range appRoleGrants {
		if _, err := inTemplate.Exec(ctx, fmt.Sprintf(grant, template, owner, app)); err != nil {
			return "", fmt.Errorf("granting the app role: %w", err)
		}
	}

	if err := postgres.Migrate(ctx, withLogin(withDatabase(server, templateName), ownerRole, ownerPassword)); err != nil {
		return "", fmt.Errorf("migrating the template: %w", err)
	}
	return server, nil
}

func createRolesAs(ctx context.Context, admin *pgx.Conn) error {
	tx, err := admin.Begin(ctx)
	if err != nil {
		return fmt.Errorf("creating the roles: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for setting, value := range map[string]string{
		"postgrestest.owner_role":     ownerRole,
		"postgrestest.app_role":       appRole,
		"postgrestest.owner_password": ownerPassword,
		"postgrestest.app_password":   appPassword,
	} {
		if _, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", setting, value); err != nil {
			return fmt.Errorf("binding %s: %w", setting, err)
		}
	}
	if _, err := tx.Exec(ctx, createRoles); err != nil {
		return fmt.Errorf("creating the roles: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("creating the roles: %w", err)
	}
	return nil
}

func withLogin(server, user, password string) string {
	parsed, err := url.Parse(server)
	if err != nil {
		return server
	}
	parsed.User = url.UserPassword(user, password)
	return parsed.String()
}

func withDatabase(server, name string) string {
	parsed, err := url.Parse(server)
	if err != nil {
		return server
	}
	parsed.Path = "/" + strings.TrimPrefix(name, "/")
	return parsed.String()
}
