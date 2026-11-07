-- The rows the lookup tables start with. Run on every open, so a database created
-- before a kind or a status existed gains it.
INSERT OR IGNORE INTO project_kinds (name) VALUES ('build'), ('chore'), ('life');

INSERT OR IGNORE INTO project_statuses (name)
VALUES ('active'), ('someday'), ('completed'), ('dropped');
