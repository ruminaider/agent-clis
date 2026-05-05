-- 0003_canonical_path_hash: add canonical_path_hash column and indexes
-- to intent_paths, change_paths, and conflicts. SPEC §14 #8.
--
-- Background: path_hash = sha256(NFC(realpath)) embedded the absolute
-- worktree directory in the equality key, so the same logical file
-- claimed from two different git worktrees of the same repo produced
-- two distinct hashes. Conflict detection silently missed cross-
-- worktree overlaps even though both rows shared the same display
-- path. SPEC §0.4 explicitly lists multi-worktree support as a goal.
--
-- The new column canonical_path_hash = sha256(NFC(case-fold(display)))
-- is the equality key for conflict detection, lock sentinel naming,
-- and lookups. Case folding uses Unicode-aware folding so two distinct
-- macOS APFS aliases of the same logical file collide as expected.
-- The legacy path_hash column is preserved for forensic comparisons
-- and verifier SYMLINK_ALIAS checks.
--
-- This SQL migration adds the columns nullable so that pre-existing
-- rows are clearly marked as not-yet-backfilled. The Go-side migration
-- step in storage.Migrator.Up performs the backfill in the same Apply
-- pass, refuses to backfill while any intent is `active` unless an
-- override is supplied, and sweeps stale lock sentinels keyed by the
-- old hash. Re-running the migration is idempotent.

ALTER TABLE intent_paths ADD COLUMN canonical_path_hash TEXT;
ALTER TABLE change_paths ADD COLUMN canonical_path_hash TEXT;
ALTER TABLE conflicts    ADD COLUMN canonical_path_hash TEXT;

CREATE INDEX IF NOT EXISTS idx_intent_paths_canonical ON intent_paths(canonical_path_hash);
CREATE INDEX IF NOT EXISTS idx_change_paths_canonical ON change_paths(canonical_path_hash);
CREATE INDEX IF NOT EXISTS idx_conflicts_canonical    ON conflicts(canonical_path_hash);
