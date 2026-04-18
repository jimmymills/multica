-- name: AddIssueReaction :one
INSERT INTO issue_reaction (issue_id, workspace_id, actor_type, actor_id, emoji)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (issue_id, actor_type, actor_id, emoji) DO UPDATE SET created_at = issue_reaction.created_at
RETURNING *;

-- name: RemoveIssueReaction :exec
DELETE FROM issue_reaction
WHERE issue_id = $1 AND actor_type = $2 AND actor_id = $3 AND emoji = $4;

-- name: ListIssueReactions :many
SELECT * FROM issue_reaction
WHERE issue_id = $1
ORDER BY created_at ASC;

-- name: GetIssueReactionByKey :one
-- Used by the DELETE write-through path to look up the local cache row
-- (including its gitlab_award_id) before calling GitLab.
SELECT * FROM issue_reaction
WHERE issue_id = $1 AND actor_type = $2 AND actor_id = $3 AND emoji = $4
LIMIT 1;

-- name: DeleteIssueReactionByID :exec
DELETE FROM issue_reaction WHERE id = $1;
