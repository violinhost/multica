CREATE UNIQUE INDEX CONCURRENTLY uq_ci_repository_profile_scope
    ON ci_repository_profile (workspace_id, project_id, resource_id, schema_version);
