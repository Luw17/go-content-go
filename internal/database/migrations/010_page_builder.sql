CREATE TABLE page_layouts (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    blocks_json TEXT NOT NULL DEFAULT '[]',
    is_homepage INTEGER NOT NULL DEFAULT 0 CHECK (is_homepage IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX page_layouts_homepage_idx ON page_layouts (is_homepage);

CREATE TABLE site_sections (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    section_type TEXT NOT NULL CHECK (section_type IN ('header', 'footer')),
    target_scope TEXT NOT NULL DEFAULT 'all' CHECK (target_scope IN ('all', 'home', 'posts', 'pages', 'slug')),
    target_slug TEXT NOT NULL DEFAULT '',
    config_json TEXT NOT NULL DEFAULT '{}',
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX site_sections_lookup_idx ON site_sections (section_type, active, target_scope, target_slug);

CREATE TABLE form_submissions (
    id INTEGER PRIMARY KEY,
    form_id TEXT NOT NULL,
    page_path TEXT NOT NULL DEFAULT '/',
    sender_name TEXT NOT NULL DEFAULT '',
    sender_email TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX form_submissions_created_idx ON form_submissions (created_at DESC, id DESC);

ALTER TABLE posts ADD COLUMN layout_id INTEGER REFERENCES page_layouts(id) ON DELETE SET NULL;
