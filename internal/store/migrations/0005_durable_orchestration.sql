CREATE TABLE orchestration_state (
    id INTEGER PRIMARY KEY CHECK(id = 1),
    version INTEGER NOT NULL,
    manager_json TEXT NOT NULL,
    scheduler_json TEXT NOT NULL
);
CREATE TABLE orchestration_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
);
CREATE INDEX idx_outbox_pending ON orchestration_outbox(status, id);
CREATE TABLE agent_event_cursors (
    agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE agent_hook_tokens (
    agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL
);
-- Old harness/user events reused sequence values. Give legacy rows a unique
-- stable order above every old cursor before enforcing the durable stream key.
WITH ranked AS MATERIALIZED (
    SELECT id, ROW_NUMBER() OVER(PARTITION BY agent_id ORDER BY id)
        + MAX(seq) OVER(PARTITION BY agent_id) AS normalized
    FROM events
)
UPDATE events SET seq=(SELECT normalized FROM ranked WHERE ranked.id=events.id);
CREATE UNIQUE INDEX idx_events_agent_sequence ON events(agent_id,seq);
