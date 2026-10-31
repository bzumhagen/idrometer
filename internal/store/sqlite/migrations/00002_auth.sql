-- +goose Up
CREATE TABLE users (
    username      TEXT PRIMARY KEY,
    password_hash TEXT    NOT NULL,
    created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    username   TEXT    NOT NULL REFERENCES users(username) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX sessions_expires_at ON sessions(expires_at);

-- +goose Down
DROP TABLE sessions;
DROP TABLE users;
