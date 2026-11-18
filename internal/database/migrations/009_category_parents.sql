-- Migration 009: many-to-many category parents
CREATE TABLE IF NOT EXISTS category_parents (
    category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    parent_id   INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    PRIMARY KEY (category_id, parent_id)
);
CREATE INDEX IF NOT EXISTS idx_catpar_cat    ON category_parents(category_id);
CREATE INDEX IF NOT EXISTS idx_catpar_parent ON category_parents(parent_id);

-- Media folder hierarchy
CREATE TABLE IF NOT EXISTS media_folders (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    name      TEXT    NOT NULL,
    parent_id INTEGER REFERENCES media_folders(id) ON DELETE CASCADE
);

-- Media metadata columns
ALTER TABLE media ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE media ADD COLUMN folder_id   INTEGER REFERENCES media_folders(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_media_folder ON media(folder_id);
