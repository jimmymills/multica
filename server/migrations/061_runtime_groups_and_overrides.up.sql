CREATE TABLE runtime_group (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE TABLE runtime_group_member (
    group_id UUID NOT NULL REFERENCES runtime_group(id) ON DELETE CASCADE,
    runtime_id UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, runtime_id)
);

CREATE INDEX idx_runtime_group_member_runtime ON runtime_group_member(runtime_id);

CREATE TABLE agent_runtime_group (
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    group_id UUID NOT NULL REFERENCES runtime_group(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, group_id)
);

CREATE INDEX idx_agent_runtime_group_group ON agent_runtime_group(group_id);

CREATE TABLE runtime_group_override (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    runtime_id UUID NOT NULL,
    starts_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ends_at TIMESTAMPTZ NOT NULL,
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (group_id, runtime_id)
        REFERENCES runtime_group_member(group_id, runtime_id)
        ON DELETE CASCADE,
    CHECK (ends_at > starts_at)
);

CREATE INDEX idx_runtime_group_override_group_ends
    ON runtime_group_override(group_id, ends_at DESC);
