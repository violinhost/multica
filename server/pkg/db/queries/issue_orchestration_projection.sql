-- name: GetIssueOrchestrationProjection :one
SELECT * FROM issue_orchestration_projection
WHERE issue_id = sqlc.arg('issue_id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid;

-- name: ListIssueOrchestrationProjections :many
SELECT * FROM issue_orchestration_projection
WHERE workspace_id = sqlc.arg('workspace_id')::uuid
  AND issue_id = ANY(sqlc.arg('issue_ids')::uuid[]);

-- name: LockIssueForOrchestrationProjection :one
SELECT * FROM issue
WHERE id = sqlc.arg('id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid
FOR UPDATE;

-- name: GetOrchestrationProjectionReceipt :one
SELECT * FROM issue_orchestration_projection_receipt
WHERE producer_installation_id = sqlc.arg('producer_installation_id')::uuid
  AND receipt_id = sqlc.arg('receipt_id')::text
  AND receipt_digest = sqlc.arg('receipt_digest')::text;

-- name: UpsertIssueOrchestrationProjection :one
INSERT INTO issue_orchestration_projection (
    issue_id, workspace_id, schema_version, producer, producer_installation_id,
    receipt_id, receipt_digest, parent_issue_id, workflow_id, stage, role,
    owner_type, owner_id, substate, reason_code, since, elapsed_seconds,
    sla_posture, route_generation, authoritative_child_issue_id,
    authoritative_run_id, native_status_key, native_status_category,
    native_status_definition_id, next_action_code, next_action_target,
    issue_revision
) VALUES (
    sqlc.arg('issue_id')::uuid, sqlc.arg('workspace_id')::uuid,
    sqlc.arg('schema_version')::integer, 'automultica',
    sqlc.arg('producer_installation_id')::uuid, sqlc.arg('receipt_id')::text,
    sqlc.arg('receipt_digest')::text, sqlc.narg('parent_issue_id')::uuid,
    sqlc.arg('workflow_id')::text, sqlc.arg('stage')::text, sqlc.arg('role')::text,
    sqlc.narg('owner_type')::text, sqlc.narg('owner_id')::uuid,
    sqlc.arg('substate')::text, sqlc.arg('reason_code')::text,
    sqlc.arg('since')::timestamptz, sqlc.arg('elapsed_seconds')::bigint,
    sqlc.arg('sla_posture')::text, sqlc.arg('route_generation')::bigint,
    sqlc.narg('authoritative_child_issue_id')::uuid,
    sqlc.narg('authoritative_run_id')::uuid, sqlc.arg('native_status_key')::text,
    sqlc.arg('native_status_category')::text,
    sqlc.arg('native_status_definition_id')::uuid,
    sqlc.arg('next_action_code')::text, sqlc.narg('next_action_target')::text,
    sqlc.arg('issue_revision')::bigint
)
ON CONFLICT (issue_id) DO UPDATE SET
    schema_version = EXCLUDED.schema_version,
    producer = EXCLUDED.producer,
    producer_installation_id = EXCLUDED.producer_installation_id,
    receipt_id = EXCLUDED.receipt_id,
    receipt_digest = EXCLUDED.receipt_digest,
    parent_issue_id = EXCLUDED.parent_issue_id,
    workflow_id = EXCLUDED.workflow_id,
    stage = EXCLUDED.stage,
    role = EXCLUDED.role,
    owner_type = EXCLUDED.owner_type,
    owner_id = EXCLUDED.owner_id,
    substate = EXCLUDED.substate,
    reason_code = EXCLUDED.reason_code,
    since = EXCLUDED.since,
    elapsed_seconds = EXCLUDED.elapsed_seconds,
    sla_posture = EXCLUDED.sla_posture,
    route_generation = EXCLUDED.route_generation,
    authoritative_child_issue_id = EXCLUDED.authoritative_child_issue_id,
    authoritative_run_id = EXCLUDED.authoritative_run_id,
    native_status_key = EXCLUDED.native_status_key,
    native_status_category = EXCLUDED.native_status_category,
    native_status_definition_id = EXCLUDED.native_status_definition_id,
    next_action_code = EXCLUDED.next_action_code,
    next_action_target = EXCLUDED.next_action_target,
    issue_revision = EXCLUDED.issue_revision,
    updated_at = now()
RETURNING *;

-- name: CreateOrchestrationProjectionReceipt :one
INSERT INTO issue_orchestration_projection_receipt (
    producer_installation_id, receipt_id, receipt_digest, issue_id, route_generation
) VALUES (
    sqlc.arg('producer_installation_id')::uuid, sqlc.arg('receipt_id')::text,
    sqlc.arg('receipt_digest')::text, sqlc.arg('issue_id')::uuid,
    sqlc.arg('route_generation')::bigint
)
RETURNING *;

-- name: TouchIssueForOrchestrationProjection :one
UPDATE issue
SET revision = revision + 1,
    last_activity_at = GREATEST(COALESCE(last_activity_at, updated_at), now()),
    updated_at = now()
WHERE id = sqlc.arg('id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid
  AND revision = sqlc.arg('expected_revision')::bigint
RETURNING *;
