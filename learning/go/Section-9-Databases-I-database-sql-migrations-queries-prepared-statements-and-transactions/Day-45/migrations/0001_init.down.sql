-- 0001_init.down.sql

DROP INDEX IF EXISTS idx_notes_user_created;
DROP TABLE IF EXISTS notes;
DROP INDEX IF EXISTS idx_users_email;
DROP TABLE IF EXISTS users;
