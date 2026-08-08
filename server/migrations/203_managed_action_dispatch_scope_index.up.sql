CREATE UNIQUE INDEX CONCURRENTLY uq_managed_action_dispatch_scope
    ON managed_action_dispatch (workspace_id, project_id, parent_issue_id, action_key, idempotency_key, generation);
