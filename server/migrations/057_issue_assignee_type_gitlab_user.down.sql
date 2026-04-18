DROP INDEX IF EXISTS gitlab_project_member_id_unique;
ALTER TABLE gitlab_project_member DROP COLUMN IF EXISTS id;

ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_assignee_type_check;
-- Restore the pre-057 shape: strict IN (...) without 'gitlab_user'. The
-- column remains nullable (NULL passes CHECK automatically).
ALTER TABLE issue ADD CONSTRAINT issue_assignee_type_check
    CHECK (assignee_type IN ('member', 'agent'));
