-- changes_on_behalf_of
--
-- As whom a change was made, when that is not its actor: a platform
-- admin acting as a member (ADR 0005, "Acting as a member") records
-- actor "platform:7" and on_behalf_of "member:12". Empty otherwise.
-- Its own column, not packed into actor, so every reader of actor
-- keeps working and "what was done as Vera?" is an equality, not a
-- LIKE. An ADD COLUMN, not a rebuild, so 0001's append-only triggers
-- stay in place; the app's TestChangesIsStillAppendOnly proves it.
-- Rows already recorded keep '': nobody acted as anyone then.
ALTER TABLE `changes` ADD `on_behalf_of` text NOT NULL DEFAULT '';

CREATE INDEX `idx_changes_on_behalf_of` ON `changes`(`on_behalf_of`);
