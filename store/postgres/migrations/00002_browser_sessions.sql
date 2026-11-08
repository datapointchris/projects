-- +goose Up
-- The tables gobrowserlogin's postgresstore reads and writes, as its Schema
-- declares them. The API checks their columns at start, so a column renamed here
-- keeps the browser login from starting. Neither carries an audit trigger: a
-- session's row is written on every login and again as it is used, and no
-- person edits one.

-- A login a browser has started and not finished. state_sha256 is the SHA-256 of
-- the OAuth state, and the callback deletes the row as it reads it.
CREATE TABLE browser_login_attempts (
    state_sha256 bytea CONSTRAINT pk_browser_login_attempts PRIMARY KEY,
    nonce text NOT NULL,
    code_verifier text NOT NULL,
    return_to text NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX ix_browser_login_attempts_expires_at ON browser_login_attempts (expires_at);

-- One browser's login. cookie_sha256 is the SHA-256 of the session cookie's value,
-- so a copy of this table names no cookie a reader could present.
CREATE TABLE browser_sessions (
    cookie_sha256 bytea CONSTRAINT pk_browser_sessions PRIMARY KEY,
    identity jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    seen_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX ix_browser_sessions_expires_at ON browser_sessions (expires_at);

-- +goose Down
DROP TABLE browser_sessions;
DROP TABLE browser_login_attempts;
