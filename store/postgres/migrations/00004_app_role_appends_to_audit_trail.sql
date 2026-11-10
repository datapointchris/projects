-- +goose Up
-- The API logs in as the role named for the database with _app after it, which
-- holds SELECT, INSERT, UPDATE and DELETE on every table and owns none. The audit
-- trigger runs as the writer, so that role appends to audit_trail. Here it keeps
-- reading and appending, and loses rewriting and erasing an entry. A database with
-- no such role, such as a test's, has nothing to revoke.
-- +goose StatementBegin
DO $$
DECLARE
    app_role text := current_database() || '_app';
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = app_role) THEN
        EXECUTE format('REVOKE UPDATE, DELETE ON audit_trail FROM %I', app_role);
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
    app_role text := current_database() || '_app';
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = app_role) THEN
        EXECUTE format('GRANT UPDATE, DELETE ON audit_trail TO %I', app_role);
    END IF;
END;
$$;
-- +goose StatementEnd
