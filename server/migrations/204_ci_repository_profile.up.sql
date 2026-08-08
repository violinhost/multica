-- Native CI repository-profile aggregate (VEL-4636).  This migration follows
-- the managed-action migrations 202/203 from VEL-4633.
--
-- No foreign keys are used: a project resource can be deleted independently,
-- and application code explicitly tombstones the dependent profile first.

CREATE TABLE ci_repository_profile (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    resource_id UUID NOT NULL,
    schema_version TEXT NOT NULL CHECK (schema_version = 'ci.repository-profile.v1'),
    repository_identity TEXT NOT NULL,
    generation INTEGER NOT NULL DEFAULT 1 CHECK (generation > 0),
    revision CHAR(40) NOT NULL CHECK (revision ~ '^[0-9a-f]{40}$'),
    workflow_class TEXT NOT NULL CHECK (workflow_class = 'native_ci'),
    workflow_version TEXT NOT NULL CHECK (workflow_version = 'v1'),
    job_class TEXT NOT NULL CHECK (job_class = 'backend'),
    check_name TEXT NOT NULL CHECK (check_name = 'backend'),
    service_classes JSONB NOT NULL CHECK (service_classes = '["postgresql_pgvector", "redis"]'::jsonb),
    hosted_fallback BOOLEAN NOT NULL DEFAULT false CHECK (hosted_fallback = false),
    projection_digest CHAR(64) NOT NULL CHECK (projection_digest ~ '^[0-9a-f]{64}$'),
    status TEXT NOT NULL DEFAULT 'pending_adapter' CHECK (status IN ('pending_adapter', 'enabled', 'disabled')),
    adapter_attestation_reference TEXT,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ci_repository_profile_receipt (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    resource_id UUID NOT NULL,
    request_id TEXT NOT NULL,
    request_digest CHAR(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    profile_id UUID NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ci_repository_profile_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    resource_id UUID NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    action TEXT NOT NULL CHECK (action IN ('register', 'enable', 'disable')),
    source TEXT NOT NULL,
    actor_type TEXT NOT NULL CHECK (actor_type = 'member'),
    actor_id UUID NOT NULL,
    projection_digest CHAR(64) NOT NULL CHECK (projection_digest ~ '^[0-9a-f]{64}$'),
    attestation_reference TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
