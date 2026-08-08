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
    CHECK (state IN ('analysis_queued', 'analysis_running', 'analysis_terminal', 'succeeded', 'failed'))
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
    CHECK (status IN ('pending', 'delivered'))
);

CREATE UNIQUE INDEX CONCURRENTLY uq_managed_action_dispatch_scope
    ON managed_action_dispatch (workspace_id, project_id, parent_issue_id, action_key, idempotency_key, generation);
