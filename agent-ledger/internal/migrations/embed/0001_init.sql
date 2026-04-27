-- 0001_init: Phase 1 kernel-slice schema. See SPEC.md §11.
-- Every table is created IF NOT EXISTS so that a partial / interrupted
-- migration can be re-run safely. The migration row is the durable
-- success marker, written inside the same transaction.

CREATE TABLE IF NOT EXISTS schema_migrations (
  version    INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agents (
  agent_id         TEXT PRIMARY KEY,
  agent_kind       TEXT NOT NULL,
  harness          TEXT,
  model            TEXT,
  parent_agent_id  TEXT,
  orchestrator_id  TEXT,
  started_at       TEXT NOT NULL,
  ended_at         TEXT,
  metadata_json    TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS assignments (
  assignment_id        TEXT PRIMARY KEY,
  event_id             TEXT NOT NULL UNIQUE,
  task_id              TEXT NOT NULL,
  orchestrator_id      TEXT NOT NULL,
  assigned_agent_id    TEXT,
  harness_run_id       TEXT,
  branch               TEXT,
  base_sha             TEXT,
  allowed_paths_json   TEXT NOT NULL,
  forbidden_paths_json TEXT NOT NULL DEFAULT '[]',
  conflict_policy      TEXT NOT NULL,
  reason               TEXT NOT NULL,
  status               TEXT NOT NULL DEFAULT 'active',
  created_at           TEXT NOT NULL,
  closed_at            TEXT,
  metadata_json        TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_assignments_task_id  ON assignments(task_id);
CREATE INDEX IF NOT EXISTS idx_assignments_agent_id ON assignments(assigned_agent_id);

CREATE TABLE IF NOT EXISTS intents (
  intent_id            TEXT PRIMARY KEY,
  event_id             TEXT NOT NULL UNIQUE,
  assignment_id        TEXT,
  task_id              TEXT NOT NULL,
  agent_id             TEXT NOT NULL,
  access_mode          TEXT NOT NULL,
  conflict_policy      TEXT NOT NULL,
  reason               TEXT NOT NULL,
  status               TEXT NOT NULL DEFAULT 'active',
  opened_at            TEXT NOT NULL,
  last_heartbeat_at    TEXT,
  heartbeat_expires_at TEXT,
  closed_at            TEXT,
  close_outcome        TEXT,
  close_reason         TEXT,
  metadata_json        TEXT NOT NULL DEFAULT '{}',
  FOREIGN KEY (assignment_id) REFERENCES assignments(assignment_id),
  FOREIGN KEY (agent_id)      REFERENCES agents(agent_id)
);
CREATE INDEX IF NOT EXISTS idx_intents_task_id  ON intents(task_id);
CREATE INDEX IF NOT EXISTS idx_intents_agent_id ON intents(agent_id);
CREATE INDEX IF NOT EXISTS idx_intents_status   ON intents(status);

CREATE TABLE IF NOT EXISTS intent_paths (
  intent_id    TEXT NOT NULL,
  path         TEXT NOT NULL,
  realpath     TEXT NOT NULL,
  path_hash    TEXT NOT NULL,
  access_mode  TEXT NOT NULL,
  PRIMARY KEY (intent_id, path_hash),
  FOREIGN KEY (intent_id) REFERENCES intents(intent_id)
);
CREATE INDEX IF NOT EXISTS idx_intent_paths_path_hash ON intent_paths(path_hash);
CREATE INDEX IF NOT EXISTS idx_intent_paths_path      ON intent_paths(path);

CREATE TABLE IF NOT EXISTS changes (
  change_id      TEXT PRIMARY KEY,
  event_id       TEXT NOT NULL UNIQUE,
  intent_id      TEXT,
  assignment_id  TEXT,
  task_id        TEXT NOT NULL,
  agent_id       TEXT,
  actor_kind     TEXT NOT NULL,
  summary        TEXT NOT NULL,
  created_at     TEXT NOT NULL,
  commit_sha     TEXT,
  metadata_json  TEXT NOT NULL DEFAULT '{}',
  FOREIGN KEY (intent_id)     REFERENCES intents(intent_id),
  FOREIGN KEY (assignment_id) REFERENCES assignments(assignment_id)
);
CREATE INDEX IF NOT EXISTS idx_changes_task_id    ON changes(task_id);
CREATE INDEX IF NOT EXISTS idx_changes_agent_id   ON changes(agent_id);
CREATE INDEX IF NOT EXISTS idx_changes_created_at ON changes(created_at);

CREATE TABLE IF NOT EXISTS change_paths (
  change_id        TEXT NOT NULL,
  path             TEXT NOT NULL,
  realpath         TEXT NOT NULL,
  path_hash        TEXT NOT NULL,
  before_sha256    TEXT,
  after_sha256     TEXT,
  patch_sha256     TEXT,
  line_ranges_json TEXT NOT NULL DEFAULT '[]',
  status           TEXT NOT NULL,
  PRIMARY KEY (change_id, path_hash),
  FOREIGN KEY (change_id) REFERENCES changes(change_id)
);
CREATE INDEX IF NOT EXISTS idx_change_paths_path_hash ON change_paths(path_hash);
CREATE INDEX IF NOT EXISTS idx_change_paths_path      ON change_paths(path);

CREATE TABLE IF NOT EXISTS validations (
  validation_id  TEXT PRIMARY KEY,
  change_id      TEXT,
  task_id        TEXT NOT NULL,
  command        TEXT NOT NULL,
  status         TEXT NOT NULL,
  started_at     TEXT,
  completed_at   TEXT,
  exit_code      INTEGER,
  output_ref     TEXT,
  metadata_json  TEXT NOT NULL DEFAULT '{}',
  FOREIGN KEY (change_id) REFERENCES changes(change_id)
);
CREATE INDEX IF NOT EXISTS idx_validations_task_id ON validations(task_id);

CREATE TABLE IF NOT EXISTS conflicts (
  conflict_id              TEXT PRIMARY KEY,
  event_id                 TEXT NOT NULL UNIQUE,
  path                     TEXT NOT NULL,
  path_hash                TEXT NOT NULL,
  existing_intent_id       TEXT,
  new_intent_id            TEXT,
  policy                   TEXT NOT NULL,
  status                   TEXT NOT NULL,
  detected_at              TEXT NOT NULL,
  acknowledged_at          TEXT,
  acknowledged_by_agent_id TEXT,
  resolution               TEXT,
  metadata_json            TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_conflicts_path_hash ON conflicts(path_hash);
CREATE INDEX IF NOT EXISTS idx_conflicts_status    ON conflicts(status);

CREATE TABLE IF NOT EXISTS events (
  event_id       TEXT PRIMARY KEY,
  schema         TEXT NOT NULL,
  event_type     TEXT NOT NULL,
  created_at     TEXT NOT NULL,
  agent_id       TEXT,
  task_id        TEXT,
  payload_json   TEXT NOT NULL,
  payload_ref    TEXT,
  payload_sha256 TEXT
);
CREATE INDEX IF NOT EXISTS idx_events_type       ON events(event_type);
CREATE INDEX IF NOT EXISTS idx_events_agent_id   ON events(agent_id);
CREATE INDEX IF NOT EXISTS idx_events_task_id    ON events(task_id);
CREATE INDEX IF NOT EXISTS idx_events_created_at ON events(created_at);
