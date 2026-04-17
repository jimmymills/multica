ALTER TABLE workspace_gitlab_connection DROP COLUMN IF EXISTS last_webhook_received_at;
DROP TABLE IF EXISTS gitlab_webhook_event;
