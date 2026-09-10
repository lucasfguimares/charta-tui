CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT OR REPLACE INTO projects (id, name, status, updated_at)
VALUES
    (1, 'Charta TUI', 'active', '2026-09-09'),
    (2, 'SQL editor hardening', 'active', '2026-09-08'),
    (3, 'First public release', 'planned', '2026-09-07');

SELECT
    status,
    COUNT(*) AS projects,
    MAX(updated_at) AS last_update
FROM projects
GROUP BY status
ORDER BY projects DESC;
