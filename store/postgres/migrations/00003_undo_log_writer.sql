-- +goose Up
-- Each undo entry names its writer: the person's subject or the service's client
-- id, and the client the write came through. An undo reads only its own writer's
-- entries, so one client's undo never reverts another's write. A row written before
-- these columns holds two empty strings, which no client of the API is, so no undo
-- reaches it.
ALTER TABLE undo_log
    ADD COLUMN actor text NOT NULL DEFAULT '',
    ADD COLUMN client text NOT NULL DEFAULT '';

CREATE INDEX ix_undo_log_writer ON undo_log (actor, client, id);

-- +goose Down
DROP INDEX ix_undo_log_writer;
ALTER TABLE undo_log DROP COLUMN client, DROP COLUMN actor;
