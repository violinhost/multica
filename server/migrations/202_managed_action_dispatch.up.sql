CREATE TABLE managed_action_enablement (
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    action_key TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, project_id, action_key)
);

CREATE TABLE managed_action_dispatch (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    parent_issue_id UUID NOT NULL,
    action_key TEXT NOT NULL,
    action_version TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    generation INTEGER NOT NULL DEFAULT 1,
    workflow TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'analysis_queued',
    primary_agent_id UUID NOT NULL,
    resource_bindings JSONB NOT NULL,
    revision_facts JSONB NOT NULL,
    authority_receipt JSONB NOT NULL,
    request_snapshot JSONB NOT NULL,
    child_issue_id UUID,
    task_id UUID,
    terminal_observation JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (generation > 0),
    CHECK (state IN ('analysis_queued', 'analysis_running', 'analysis_terminal_completed', 'analysis_terminal_failed', 'analysis_terminal_cancelled', 'analysis_terminal_unknown', 'succeeded', 'failed'))
);

CREATE TABLE managed_action_outbox (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dispatch_id UUID NOT NULL,
    task_id UUID NOT NULL,
    runtime_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status IN ('pending', 'delivering', 'delivered'))
);

CREATE TABLE managed_action_lane_observation (
    dispatch_id UUID NOT NULL,
    task_id UUID NOT NULL,
    lane_role TEXT NOT NULL,
    stage INTEGER NOT NULL,
    task_status TEXT NOT NULL,
    failure_reason TEXT,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (dispatch_id, task_id),
    CHECK (lane_role = 'analysis'),
    CHECK (stage = 1),
    CHECK (task_status IN ('completed', 'failed', 'cancelled'))
);
