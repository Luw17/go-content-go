ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'admin';

CREATE TABLE sessions (
    token_hash BLOB PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
