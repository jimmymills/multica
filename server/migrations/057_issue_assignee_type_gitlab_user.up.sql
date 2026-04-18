-- Add 'gitlab_user' to the issue.assignee_type enum. This value represents
-- a GitLab-native user who assigned an issue via the GitLab UI and has no
-- Multica user mapping. assignee_id for this type is a gitlab_project_member.id.
--
-- The column is already nullable; the pre-existing CHECK was strict
-- IN ('member', 'agent') (NULLs pass CHECKs automatically). We rewrite it
-- explicitly with IS NULL OR ... for clarity and to add 'gitlab_user'.

ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_assignee_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_assignee_type_check
    CHECK (assignee_type IS NULL OR assignee_type IN ('member', 'agent', 'gitlab_user'));

-- Add a UUID id to gitlab_project_member. The existing composite PK on
-- (workspace_id, gitlab_user_id) stays; id becomes a secondary unique key
-- so other tables can reference it by UUID.
ALTER TABLE gitlab_project_member ADD COLUMN id UUID NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX gitlab_project_member_id_unique ON gitlab_project_member (id);
