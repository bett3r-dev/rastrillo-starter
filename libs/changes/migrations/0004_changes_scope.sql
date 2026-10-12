-- changes_scope
--
-- What larger thing a change belongs to ("session:12"), set by the
-- Trail with Within, so that thing finds every change of its own,
-- deletes and update diffs included, with one indexed equality: no
-- join across contexts, no parsing of diff JSON. '' means unscoped,
-- which every write was before this column and stays unless its Trail
-- says otherwise. The index on scope alone serves "this scope's
-- changes after id N, in id order": SQLite keeps the rowid (id) in
-- every index entry, so the range on id needs no second column. An
-- ADD COLUMN, not a rebuild, so 0001's append-only triggers stay in
-- place; the app's TestChangesIsStillAppendOnly proves it. Rows
-- already recorded keep ''.
ALTER TABLE `changes` ADD `scope` text NOT NULL DEFAULT '';

CREATE INDEX `idx_changes_scope` ON `changes`(`scope`);
