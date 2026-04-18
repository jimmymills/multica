-- Rollback: re-tighten. If any rows with NULL actor_type exist, rollback
-- fails — intentional data-integrity guard.
ALTER TABLE comment_reaction DROP CONSTRAINT IF EXISTS comment_reaction_actor_type_check;
ALTER TABLE comment_reaction ADD CONSTRAINT comment_reaction_actor_type_check
    CHECK (actor_type IN ('member', 'agent'));
ALTER TABLE comment_reaction ALTER COLUMN actor_id SET NOT NULL;
ALTER TABLE comment_reaction ALTER COLUMN actor_type SET NOT NULL;
