ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT -1;

CREATE TABLE user_recovery_codes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    code_hash TEXT NOT NULL,
    used_at TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX user_recovery_codes_user_idx ON user_recovery_codes (user_id);
