CREATE TABLE menus (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
    location TEXT NOT NULL UNIQUE CHECK (location IN ('primary', 'footer')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE menu_items (
    id INTEGER PRIMARY KEY,
    menu_id INTEGER NOT NULL REFERENCES menus(id) ON DELETE CASCADE,
    label TEXT NOT NULL CHECK (length(trim(label)) BETWEEN 1 AND 100),
    url TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('page', 'custom')),
    object_id INTEGER,
    item_order INTEGER NOT NULL DEFAULT 0,
    parent_id INTEGER REFERENCES menu_items(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    CHECK (
        (type = 'page' AND object_id IS NOT NULL) OR
        (type = 'custom' AND object_id IS NULL)
    )
);

CREATE INDEX menu_items_order_idx ON menu_items (menu_id, item_order, id);
