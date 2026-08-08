CREATE UNIQUE INDEX CONCURRENTLY uq_ci_repository_profile_receipt_idempotency
    ON ci_repository_profile_receipt (workspace_id, project_id, resource_id, request_id);
