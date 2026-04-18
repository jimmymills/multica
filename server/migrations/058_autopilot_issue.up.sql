-- Phase 4: maps an autopilot run to the GitLab issue it created, replacing
-- the issues.origin_type='autopilot' / origin_id lookup path. Fresh install
-- only; no backfill of pre-Phase-4 autopilot runs.

CREATE TABLE autopilot_issue (
    autopilot_run_id UUID NOT NULL REFERENCES autopilot_run(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    gitlab_iid INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (autopilot_run_id, workspace_id, gitlab_iid)
);

CREATE INDEX autopilot_issue_workspace_iid
    ON autopilot_issue (workspace_id, gitlab_iid);
