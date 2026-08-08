CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ci_repository_profile_scope
    ON ci_repository_profile (workspace_id, project_id, resource_id, revision);
