-- +goose Up
-- Lookup tables. postgres.Migrate upserts their rows from model.ProjectKinds and
-- model.ProjectStatuses on every run, so a new kind or status needs no migration.
CREATE TABLE project_kinds (
    name text PRIMARY KEY
);

CREATE TABLE project_statuses (
    name text PRIMARY KEY
);

CREATE TABLE projects (
    id uuid CONSTRAINT pk_projects PRIMARY KEY,
    name text NOT NULL,
    description text,
    kind text NOT NULL DEFAULT 'life'
    CONSTRAINT fk_projects_kind_project_kinds REFERENCES project_kinds (name),
    status text NOT NULL DEFAULT 'active'
    CONSTRAINT fk_projects_status_project_statuses REFERENCES project_statuses (name),
    status_reason text,
    closed_at timestamptz,
    position integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_projects_dropped_requires_reason CHECK (status <> 'dropped' OR status_reason IS NOT NULL)
);

-- A name is held only by the active project bearing it, so a finished project gives
-- its name back.
CREATE UNIQUE INDEX uq_projects_name_active ON projects (name) WHERE status = 'active';

CREATE SEQUENCE item_numbers AS bigint START WITH 100000;

CREATE TABLE project_items (
    id uuid CONSTRAINT pk_project_items PRIMARY KEY,
    number bigint NOT NULL DEFAULT nextval('item_numbers')
    CONSTRAINT uq_project_items_number UNIQUE,
    title text NOT NULL,
    notes text,
    repo text,
    completed boolean NOT NULL DEFAULT false,
    completed_at timestamptz,
    archived boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER SEQUENCE item_numbers OWNED BY project_items.number;

CREATE INDEX idx_pi_active ON project_items (archived) WHERE archived = false;

CREATE INDEX ix_project_items_repo ON project_items (repo);

CREATE TABLE project_item_memberships (
    item_id uuid NOT NULL
    CONSTRAINT fk_project_item_memberships_item_id_project_items REFERENCES project_items (id) ON DELETE CASCADE,
    project_id uuid NOT NULL
    CONSTRAINT fk_project_item_memberships_project_id_projects REFERENCES projects (id) ON DELETE CASCADE,
    position integer NOT NULL DEFAULT 0,
    CONSTRAINT pk_project_item_memberships PRIMARY KEY (item_id, project_id)
);

CREATE INDEX idx_pim_item ON project_item_memberships (item_id);

CREATE INDEX idx_pim_project ON project_item_memberships (project_id);

CREATE INDEX idx_pim_position ON project_item_memberships (project_id, position);

CREATE TABLE project_item_dependencies (
    item_id uuid NOT NULL
    CONSTRAINT fk_project_item_dependencies_item_id_project_items REFERENCES project_items (id) ON DELETE CASCADE,
    depends_on_id uuid NOT NULL
    CONSTRAINT fk_project_item_dependencies_depends_on_id_project_items REFERENCES project_items (id) ON DELETE CASCADE,
    CONSTRAINT pk_project_item_dependencies PRIMARY KEY (item_id, depends_on_id),
    CONSTRAINT ck_project_item_dependencies_no_self_dependency CHECK (item_id <> depends_on_id)
);

CREATE INDEX idx_pid_item ON project_item_dependencies (item_id);

CREATE INDEX idx_pid_depends ON project_item_dependencies (depends_on_id);

CREATE TABLE project_item_tasks (
    id uuid CONSTRAINT pk_project_item_tasks PRIMARY KEY,
    item_id uuid NOT NULL
    CONSTRAINT fk_project_item_tasks_item_id_project_items REFERENCES project_items (id) ON DELETE CASCADE,
    title text NOT NULL,
    completed boolean NOT NULL DEFAULT false,
    completed_at timestamptz,
    position integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_pit_item ON project_item_tasks (item_id);

CREATE INDEX idx_pit_position ON project_item_tasks (item_id, position);

-- previous_state is the service's own JSON, which this database holds as text. It
-- carries no audit trigger, because it would copy whole rows on every write and the
-- rows it describes are audited already.
CREATE TABLE undo_log (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    action text NOT NULL,
    entity_type text NOT NULL,
    entity_id text NOT NULL,
    previous_state text,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Every insert, delete and changing update to a row of an audited table leaves one
-- entry here, whatever wrote it: a statement, a cascade, or a person at a SQL prompt.
-- traceparent and baggage join an entry to the trace of the request that wrote it.
CREATE TABLE audit_trail (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    changed_at timestamptz NOT NULL DEFAULT now(),
    table_name text NOT NULL,
    record_id text NOT NULL,
    operation text NOT NULL,
    changes jsonb NOT NULL,
    actor text,
    request_id text,
    job_run_id text,
    traceparent text,
    baggage text,
    CONSTRAINT ck_audit_trail_operation_is_a_write CHECK (operation IN ('insert', 'update', 'delete'))
);

CREATE INDEX idx_audit_trail_record ON audit_trail (table_name, record_id, changed_at);

CREATE INDEX ix_audit_trail_changed_at ON audit_trail (changed_at);

CREATE INDEX ix_audit_trail_actor ON audit_trail (actor);

-- +goose StatementBegin
-- Holds each written column's old and new value. A trigger's arguments name columns
-- to strip. An update that changes nothing leaves no entry. Who wrote it comes from
-- transaction-local settings, so they end with the transaction that set them.
CREATE FUNCTION audit_trail_record() RETURNS trigger LANGUAGE plpgsql AS $$
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

CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON project_kinds
FOR EACH ROW EXECUTE FUNCTION audit_trail_record();

CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON project_statuses
FOR EACH ROW EXECUTE FUNCTION audit_trail_record();

CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON projects
FOR EACH ROW EXECUTE FUNCTION audit_trail_record();

CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON project_items
FOR EACH ROW EXECUTE FUNCTION audit_trail_record();

CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON project_item_memberships
FOR EACH ROW EXECUTE FUNCTION audit_trail_record();

CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON project_item_dependencies
FOR EACH ROW EXECUTE FUNCTION audit_trail_record();

CREATE TRIGGER audit_trail AFTER INSERT OR UPDATE OR DELETE ON project_item_tasks
FOR EACH ROW EXECUTE FUNCTION audit_trail_record();

-- +goose Down
DROP TABLE audit_trail;
DROP FUNCTION audit_trail_record();
DROP TABLE undo_log;
DROP TABLE project_item_tasks;
DROP TABLE project_item_dependencies;
DROP TABLE project_item_memberships;
DROP TABLE project_items;
DROP TABLE projects;
DROP TABLE project_statuses;
DROP TABLE project_kinds;
