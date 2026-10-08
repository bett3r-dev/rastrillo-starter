-- changes
--
-- The change record: one row per explained write on a tracked table,
-- every column NOT NULL. request_id is the root of the row's cause
-- chain, so "what started this?" is one column, never a walk.
--
-- Append-only by trigger, not by a GORM hook, so raw SQL is refused
-- too: nothing in or out of the app can rewrite history. A later
-- migration that rebuilds this table drops these triggers with it and
-- must re-create them (the app's TestChangesIsStillAppendOnly holds
-- that).
CREATE TABLE `changes` (
  `id` integer PRIMARY KEY AUTOINCREMENT,
  `reason` text NOT NULL,
  `cause` text NOT NULL,
  `request_id` text NOT NULL,
  `correlation_id` text NOT NULL,
  `actor` text NOT NULL,
  `entity` text NOT NULL,
  `entity_id` integer NOT NULL,
  `op` text NOT NULL,
  `diff` text NOT NULL,
  `created_at` datetime NOT NULL
);

CREATE INDEX `idx_changes_reason` ON `changes`(`reason`);

CREATE INDEX `idx_changes_request_id` ON `changes`(`request_id`);

CREATE INDEX `idx_changes_correlation_id` ON `changes`(`correlation_id`);

CREATE INDEX `idx_changes_entity` ON `changes`(`entity`);

CREATE INDEX `idx_changes_entity_id` ON `changes`(`entity_id`);

CREATE TRIGGER changes_no_update BEFORE UPDATE ON changes
BEGIN
  SELECT RAISE(ABORT, 'changes is append-only: no UPDATE');
END;

CREATE TRIGGER changes_no_delete BEFORE DELETE ON changes
BEGIN
  SELECT RAISE(ABORT, 'changes is append-only: no DELETE');
END;
