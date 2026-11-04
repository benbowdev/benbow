CREATE TABLE agent_sessions (
    id TEXT PRIMARY KEY,
    site TEXT NOT NULL REFERENCES sites(name) ON DELETE CASCADE,
    opened_at TEXT NOT NULL,
    last_active TEXT NOT NULL,
    agent TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT '',
    base_snapshot TEXT NOT NULL DEFAULT '',
    db_saved INTEGER NOT NULL DEFAULT 0,
    db_pid INTEGER NOT NULL DEFAULT 0,
    write_grant INTEGER NOT NULL DEFAULT 0,
    launched_tool TEXT NOT NULL DEFAULT '',
    agent_running INTEGER NOT NULL DEFAULT 0,
    closed_at TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX agent_sessions_open ON agent_sessions(site) WHERE closed_at = '';
ALTER TABLE snapshots ADD COLUMN session_id TEXT NOT NULL DEFAULT '';
