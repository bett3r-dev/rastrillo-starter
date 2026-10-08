-- init
--
-- The example app's tables: members (the accounts context) and notes
-- (the notes context). The changes table is libs/changes' own set,
-- merged into BootSchema, never here.
CREATE TABLE `members` (`id` integer PRIMARY KEY AUTOINCREMENT,`email` text,`name` text,`password_hash` text,`created_at` datetime,`updated_at` datetime);

CREATE UNIQUE INDEX `idx_members_email` ON `members`(`email`);

CREATE TABLE `notes` (`id` integer PRIMARY KEY AUTOINCREMENT,`author_id` integer,`title` text,`body` text,`created_at` datetime,`updated_at` datetime);

CREATE INDEX `idx_notes_author_id` ON `notes`(`author_id`);

