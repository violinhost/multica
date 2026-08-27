-- Receipt-bound Automultica orchestration projection. This table intentionally
-- does not own or write issue.status: it is an optional, audited read model.
CREATE TABLE issue_orchestration_projection (
    issue_id UUID PRIMARY KEY,
    workspace_id UUID NOT NULL,
    schema_version INTEGER NOT NULL,
    producer TEXT NOT NULL CHECK (producer = 'automultica'),
    producer_installation_id UUID NOT NULL,
    receipt_id TEXT NOT NULL,
    receipt_digest TEXT NOT NULL,
    parent_issue_id UUID NULL,
    workflow_id TEXT NOT NULL,
    stage TEXT NOT NULL,
    role TEXT NOT NULL,
    owner_type TEXT NULL,
    owner_id UUID NULL,
    substate TEXT NOT NULL,
    reason_code TEXT NOT NULL,
    since TIMESTAMPTZ NOT NULL,
    elapsed_seconds BIGINT NOT NULL CHECK (elapsed_seconds >= 0),
    sla_posture TEXT NOT NULL CHECK (sla_posture IN ('within_sla', 'at_risk', 'breached', 'unknown')),
    route_generation BIGINT NOT NULL CHECK (route_generation > 0),
    authoritative_child_issue_id UUID NULL,
    authoritative_run_id UUID NULL,
    native_status_key TEXT NOT NULL,
    native_status_category TEXT NOT NULL,
    native_status_definition_id UUID NOT NULL,
    next_action_code TEXT NOT NULL,
    next_action_target TEXT NULL,
    issue_revision BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((owner_type IS NULL) = (owner_id IS NULL))
);

CREATE TABLE issue_orchestration_projection_receipt (
    producer_installation_id UUID NOT NULL,
    receipt_id TEXT NOT NULL,
    receipt_digest TEXT NOT NULL,
    issue_id UUID NOT NULL,
    route_generation BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (producer_installation_id, receipt_id, receipt_digest)
);
