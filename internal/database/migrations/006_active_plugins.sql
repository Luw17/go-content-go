INSERT INTO options (key, value, updated_at)
VALUES ('active_plugins', '[]', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
ON CONFLICT(key) DO NOTHING;
