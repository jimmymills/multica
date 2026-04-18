-- Allow NULL actor_type/actor_id on comment_reaction for webhook-origin
-- reactions from unmapped GitLab users. Mirror migration 051's relaxation
-- of comment.author_type and issue_reaction.actor_type.
ALTER TABLE comment_reaction ALTER COLUMN actor_type DROP NOT NULL;
ALTER TABLE comment_reaction ALTER COLUMN actor_id DROP NOT NULL;

-- Replace the enumerated NOT NULL check with a NULL-permissive one.
ALTER TABLE comment_reaction DROP CONSTRAINT IF EXISTS comment_reaction_actor_type_check;
ALTER TABLE comment_reaction ADD CONSTRAINT comment_reaction_actor_type_check
    CHECK (actor_type IS NULL OR actor_type IN ('member', 'agent'));
