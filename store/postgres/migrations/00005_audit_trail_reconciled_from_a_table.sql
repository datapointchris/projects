-- +goose Up
-- What the trail records of each table is a row of audit_trail_tables, keyed on the
-- table's schema and name, so a table is declared before it exists. A table with no
-- row is recorded whole. Migrate calls audit_trail_reconcile() after the migrations,
-- which attaches the trigger to every table recorded and drops it from every table
-- that opts out, so a migration creating a table writes no trigger of its own.
CREATE TABLE audit_trail_tables (
    table_schema text NOT NULL,
    table_name text NOT NULL,
    is_recorded boolean NOT NULL DEFAULT true,
    ignored_columns text[] NOT NULL DEFAULT '{}',
    PRIMARY KEY (table_schema, table_name)
);

-- The trail itself, the browser login's sessions and attempts, and undo_log, which
-- copies whole rows the real tables already audit.
INSERT INTO audit_trail_tables (table_schema, table_name, is_recorded) VALUES
    ('public', 'audit_trail', false),
    ('public', 'browser_login_attempts', false),
    ('public', 'browser_sessions', false),
    ('public', 'undo_log', false);

-- Each table beside its declaration, and whether its trigger is attached.
CREATE VIEW audit_trail_targets AS
SELECT
    c.oid::regclass AS audited_table,
    coalesce(d.is_recorded, true) AS is_recorded,
    coalesce(d.ignored_columns, '{}') AS ignored_columns,
    EXISTS (SELECT FROM pg_catalog.pg_trigger AS t WHERE t.tgrelid = c.oid AND t.tgname = 'audit_trail') AS is_attached
FROM pg_catalog.pg_class AS c
INNER JOIN pg_catalog.pg_namespace AS n ON c.relnamespace = n.oid
LEFT JOIN audit_trail_tables AS d ON n.nspname = d.table_schema AND c.relname = d.table_name
WHERE c.relkind IN ('r', 'p')
    AND NOT c.relispartition
    AND n.nspname NOT IN ('pg_catalog', 'information_schema')
    AND n.nspname NOT LIKE 'pg\_%'
    AND c.relname NOT IN ('goose_db_version', 'audit_trail_tables');

-- +goose StatementBegin
-- Refuses a recorded column named like a credential, then attaches or drops each
-- table's trigger to match its declaration.
CREATE FUNCTION audit_trail_reconcile() RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    copied text;
    target audit_trail_targets;
BEGIN
    SELECT string_agg(format('%s.%I', t.audited_table, a.attname), ', ') INTO copied
    FROM audit_trail_targets AS t
    JOIN pg_attribute AS a ON a.attrelid = t.audited_table AND a.attnum > 0 AND NOT a.attisdropped
    WHERE t.is_recorded
        AND a.attname ~ '(^|_)(password|secret|token|api_key)$'
        AND a.attname <> ALL (t.ignored_columns);
    IF copied IS NOT NULL THEN
        RAISE EXCEPTION 'the audit trail would copy %; list each in its table''s ignored_columns', copied;
    END IF;

    FOR target IN SELECT * FROM audit_trail_targets WHERE is_recorded <> is_attached LOOP
        IF target.is_recorded THEN
            EXECUTE format('CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON %s '
                           'FOR EACH ROW EXECUTE FUNCTION audit_trail_record()', target.audited_table);
        ELSE
            EXECUTE format('DROP TRIGGER audit_trail ON %s', target.audited_table);
        END IF;
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Runs as its owner, so the app role needs no write on the trail. The search path is
-- fixed, so a caller's own schema cannot stand in for the trail.
CREATE OR REPLACE FUNCTION audit_trail_record() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp AS $$
DECLARE
    ignored text[] := coalesce(
        (SELECT ignored_columns FROM audit_trail_tables WHERE table_schema = TG_TABLE_SCHEMA AND table_name = TG_TABLE_NAME),
        '{}'
    );
    old_row jsonb := CASE WHEN TG_OP <> 'INSERT' THEN to_jsonb(OLD) - ignored END;
    new_row jsonb := CASE WHEN TG_OP <> 'DELETE' THEN to_jsonb(NEW) - ignored END;
    primary_key_text text;
    written jsonb;
BEGIN
    SELECT string_agg(coalesce(new_row, old_row) ->> a.attname, ',' ORDER BY k.position) INTO primary_key_text
    FROM pg_index AS i
    CROSS JOIN LATERAL unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, position)
    JOIN pg_attribute AS a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
    WHERE i.indrelid = TG_RELID AND i.indisprimary;

    IF TG_OP = 'INSERT' THEN
        SELECT jsonb_object_agg(n.key, jsonb_build_object('new', n.value)) INTO written FROM jsonb_each(new_row) AS n;
    ELSIF TG_OP = 'DELETE' THEN
        SELECT jsonb_object_agg(o.key, jsonb_build_object('old', o.value)) INTO written FROM jsonb_each(old_row) AS o;
    ELSE
        SELECT jsonb_object_agg(n.key, jsonb_build_object('old', o.value, 'new', n.value)) INTO written
        FROM jsonb_each(new_row) AS n JOIN jsonb_each(old_row) AS o ON o.key = n.key
        WHERE n.value IS DISTINCT FROM o.value;
    END IF;
    IF written IS NULL THEN
        RETURN NULL;
    END IF;

    INSERT INTO audit_trail
        (table_name, record_id, operation, changes, actor, request_id, job_run_id, traceparent, baggage)
    VALUES (
        CASE WHEN TG_TABLE_SCHEMA = 'public' THEN TG_TABLE_NAME ELSE TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME END,
        coalesce(primary_key_text, ''),
        lower(TG_OP),
        written,
        nullif(current_setting('projects.actor', true), ''),
        nullif(current_setting('projects.request_id', true), ''),
        nullif(current_setting('projects.job_run_id', true), ''),
        nullif(current_setting('projects.traceparent', true), ''),
        nullif(current_setting('projects.baggage', true), '')
    );
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The app role reads the trail and its declarations and writes neither. A database with
-- no such role, such as a test's, has nothing to narrow.
DO $$
DECLARE
    app_role text := current_database() || '_app';
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = app_role) THEN
        EXECUTE format('REVOKE INSERT, UPDATE, DELETE ON audit_trail, audit_trail_tables FROM %I', app_role);
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
        EXECUTE format('GRANT INSERT ON audit_trail TO %I', app_role);
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit_trail_record() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    old_row jsonb := CASE WHEN TG_OP <> 'INSERT' THEN to_jsonb(OLD) END;
    new_row jsonb := CASE WHEN TG_OP <> 'DELETE' THEN to_jsonb(NEW) END;
    primary_key_text text;
    written jsonb;
BEGIN
    SELECT string_agg(coalesce(new_row, old_row) ->> a.attname, ',' ORDER BY k.position) INTO primary_key_text
    FROM pg_index AS i
    CROSS JOIN LATERAL unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, position)
    JOIN pg_attribute AS a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
    WHERE i.indrelid = TG_RELID AND i.indisprimary;

    old_row := old_row - coalesce(TG_ARGV, '{}'::text[]);
    new_row := new_row - coalesce(TG_ARGV, '{}'::text[]);
    IF TG_OP = 'INSERT' THEN
        SELECT jsonb_object_agg(n.key, jsonb_build_object('new', n.value)) INTO written FROM jsonb_each(new_row) AS n;
    ELSIF TG_OP = 'DELETE' THEN
        SELECT jsonb_object_agg(o.key, jsonb_build_object('old', o.value)) INTO written FROM jsonb_each(old_row) AS o;
    ELSE
        SELECT jsonb_object_agg(n.key, jsonb_build_object('old', o.value, 'new', n.value)) INTO written
        FROM jsonb_each(new_row) AS n JOIN jsonb_each(old_row) AS o ON o.key = n.key
        WHERE n.value IS DISTINCT FROM o.value;
    END IF;
    IF written IS NULL THEN
        RETURN NULL;
    END IF;

    INSERT INTO audit_trail
        (table_name, record_id, operation, changes, actor, request_id, job_run_id, traceparent, baggage)
    VALUES (
        CASE WHEN TG_TABLE_SCHEMA = 'public' THEN TG_TABLE_NAME ELSE TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME END,
        coalesce(primary_key_text, ''),
        lower(TG_OP),
        written,
        nullif(current_setting('projects.actor', true), ''),
        nullif(current_setting('projects.request_id', true), ''),
        nullif(current_setting('projects.job_run_id', true), ''),
        nullif(current_setting('projects.traceparent', true), ''),
        nullif(current_setting('projects.baggage', true), '')
    );
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

DROP FUNCTION audit_trail_reconcile();
DROP VIEW audit_trail_targets;
DROP TABLE audit_trail_tables;
