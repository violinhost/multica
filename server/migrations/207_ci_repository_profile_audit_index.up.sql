CREATE INDEX CONCURRENTLY idx_ci_repository_profile_audit_profile_created
    ON ci_repository_profile_audit (profile_id, created_at);
