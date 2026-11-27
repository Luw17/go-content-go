CREATE TABLE redirects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_path TEXT NOT NULL UNIQUE,
    target_path TEXT NOT NULL,
    status_code INTEGER NOT NULL DEFAULT 301 CHECK (status_code IN (301, 302)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX redirects_source_idx ON redirects (source_path);

CREATE VIRTUAL TABLE posts_fts USING fts5(
    title,
    content,
    slug,
    content='posts',
    content_rowid='id'
);

CREATE TRIGGER posts_fts_ai AFTER INSERT ON posts BEGIN
    INSERT INTO posts_fts(rowid, title, content, slug)
    VALUES (new.id, new.title, new.content, new.slug);
END;

CREATE TRIGGER posts_fts_ad AFTER DELETE ON posts BEGIN
    INSERT INTO posts_fts(posts_fts, rowid, title, content, slug)
    VALUES ('delete', old.id, old.title, old.content, old.slug);
END;

CREATE TRIGGER posts_fts_au AFTER UPDATE ON posts BEGIN
    INSERT INTO posts_fts(posts_fts, rowid, title, content, slug)
    VALUES ('delete', old.id, old.title, old.content, old.slug);
    INSERT INTO posts_fts(rowid, title, content, slug)
    VALUES (new.id, new.title, new.content, new.slug);
END;

INSERT INTO posts_fts(posts_fts) VALUES('rebuild');
