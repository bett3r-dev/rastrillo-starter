-- changes_entry_point
--
-- The business name of what opened each change's write (WriteNote,
-- BankReportedTransaction): the Command at the head of the flow chain
-- in an app's docs/flow.md (ADR 0003). An ADD COLUMN, not a rebuild,
-- so 0001's append-only triggers stay in place; the app's
-- TestChangesIsStillAppendOnly proves it. Rows already recorded keep
-- "": nothing recorded what opened them, and the record is not
-- rewritten to guess.
ALTER TABLE `changes` ADD `entry_point` text NOT NULL DEFAULT "";

CREATE INDEX `idx_changes_entry_point` ON `changes`(`entry_point`);
