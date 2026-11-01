CREATE TABLE media (
    id INTEGER PRIMARY KEY,
    filename TEXT NOT NULL,
    filepath TEXT NOT NULL UNIQUE,
    file_type TEXT NOT NULL,
    file_size INTEGER NOT NULL CHECK (file_size > 0),
    alt_text TEXT NOT NULL DEFAULT '',
    uploaded_at TEXT NOT NULL
);

CREATE INDEX media_uploaded_at_idx ON media (uploaded_at DESC, id DESC);
