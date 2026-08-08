package ciprofile

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// TxStarter is deliberately narrow so the aggregate can be exercised against
// a real PostgreSQL pool without depending on HTTP or sqlc generated types.
type TxStarter interface {
	Begin(context.Context) (pgx.Tx, error)
}

type Scope struct {
	WorkspaceID pgtype.UUID
	ProjectID   pgtype.UUID
	ResourceID  pgtype.UUID
	Repository  string
	ActorID     pgtype.UUID
}

type Profile struct {
	ProfileID        string `json:"profile_id,omitempty"`
	Generation       int32  `json:"generation,omitempty"`
	Repository       string `json:"repository,omitempty"`
	Revision         string `json:"revision,omitempty"`
	ProjectionDigest string `json:"projection_digest,omitempty"`
	Status           string `json:"status,omitempty"`
	Eligible         bool   `json:"eligible"`
	Reason           string `json:"reason"`
	ReceiptID        string `json:"receipt_id,omitempty"`
}

type Aggregate struct {
	store    TxStarter
	verifier Verifier
}

func NewAggregate(store TxStarter, verifier Verifier) *Aggregate {
	return &Aggregate{store: store, verifier: verifier}
}

func (a *Aggregate) Register(ctx context.Context, scope Scope, request Registration) (Profile, bool, error) {
	if a == nil || a.store == nil {
		return Profile{}, false, ErrStoreUnavailable
	}
	projectionDigest, err := ProjectionDigest(scope.Repository, request)
	if err != nil {
		return Profile{}, false, err
	}
	requestDigest, err := RequestDigest(scope.Repository, request)
	if err != nil {
		return Profile{}, false, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		profile, created, retry, err := a.registerOnce(ctx, scope, request, projectionDigest, requestDigest)
		if retry {
			continue
		}
		return profile, created, err
	}
	return Profile{}, false, fmt.Errorf("%w: concurrent registration did not converge", ErrIdempotencyConflict)
}

func (a *Aggregate) registerOnce(ctx context.Context, scope Scope, request Registration, projectionDigest, requestDigest string) (Profile, bool, bool, error) {
	tx, err := a.store.Begin(ctx)
	if err != nil {
		return Profile{}, false, false, fmt.Errorf("begin profile registration: %w", err)
	}
	defer tx.Rollback(ctx)

	var receiptID, profileID string
	var receiptDigest, revision, digest, status string
	var generation int32
	err = tx.QueryRow(ctx, `
		SELECT r.id, r.profile_id, p.generation, r.request_digest,
		       p.revision, p.projection_digest, p.status
		FROM ci_repository_profile_receipt r
		JOIN ci_repository_profile p ON p.id = r.profile_id
		WHERE r.workspace_id=$1 AND r.project_id=$2 AND r.resource_id=$3 AND r.request_id=$4`,
		scope.WorkspaceID, scope.ProjectID, scope.ResourceID, request.RequestID,
	).Scan(&receiptID, &profileID, &generation, &receiptDigest, &revision, &digest, &status)
	if err == nil {
		if receiptDigest != requestDigest {
			return Profile{}, false, false, ErrIdempotencyConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Profile{}, false, false, err
		}
		return profileResponse(profileID, receiptID, generation, scope.Repository, revision, digest, status, revision), false, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, false, false, err
	}

	var existingDigest string
	err = tx.QueryRow(ctx, `SELECT id, generation, revision, projection_digest, status
		FROM ci_repository_profile
		WHERE workspace_id=$1 AND project_id=$2 AND resource_id=$3 AND schema_version=$4 FOR UPDATE`,
		scope.WorkspaceID, scope.ProjectID, scope.ResourceID, SchemaVersion,
	).Scan(&profileID, &generation, &revision, &existingDigest, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO ci_repository_profile
			(workspace_id,project_id,resource_id,schema_version,repository_identity,revision,workflow_class,workflow_version,job_class,check_name,service_classes,hosted_fallback,projection_digest,created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,false,$12,$13)
			RETURNING id,generation`,
			scope.WorkspaceID, scope.ProjectID, scope.ResourceID, SchemaVersion, scope.Repository, request.Revision, request.WorkflowClass, request.WorkflowVersion, request.JobClass, request.CheckName, `["postgresql_pgvector","redis"]`, projectionDigest, scope.ActorID,
		).Scan(&profileID, &generation)
		revision, status = request.Revision, "pending_adapter"
	} else if err == nil && existingDigest != projectionDigest {
		err = tx.QueryRow(ctx, `UPDATE ci_repository_profile
			SET generation=generation+1, repository_identity=$2, revision=$3, projection_digest=$4,
			    status='pending_adapter', adapter_attestation_reference=NULL, updated_at=now()
			WHERE id=$1 RETURNING generation`, profileID, scope.Repository, request.Revision, projectionDigest).Scan(&generation)
		revision, status = request.Revision, "pending_adapter"
	}
	if err != nil {
		if isUniqueViolation(err) {
			return Profile{}, false, true, nil
		}
		return Profile{}, false, false, err
	}
	if existingDigest == projectionDigest && revision == "" {
		revision = request.Revision
	}
	if status == "" {
		status = "pending_adapter"
	}
	err = tx.QueryRow(ctx, `INSERT INTO ci_repository_profile_receipt
		(workspace_id,project_id,resource_id,request_id,request_digest,profile_id,generation,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		scope.WorkspaceID, scope.ProjectID, scope.ResourceID, request.RequestID, requestDigest, profileID, generation, scope.ActorID,
	).Scan(&receiptID)
	if err != nil {
		if isUniqueViolation(err) {
			return Profile{}, false, true, nil
		}
		return Profile{}, false, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ci_repository_profile_audit
		(profile_id,workspace_id,project_id,resource_id,generation,action,source,actor_type,actor_id,projection_digest)
		VALUES ($1,$2,$3,$4,$5,'register',$6,'member',$7,$8)`,
		profileID, scope.WorkspaceID, scope.ProjectID, scope.ResourceID, generation, request.Source, scope.ActorID, projectionDigest,
	); err != nil {
		return Profile{}, false, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, false, false, err
	}
	return profileResponse(profileID, receiptID, generation, scope.Repository, revision, projectionDigest, status, revision), true, false, nil
}

func (a *Aggregate) Discover(ctx context.Context, scope Scope, exactRevision string) (Profile, error) {
	if a == nil || a.store == nil {
		return Profile{}, ErrStoreUnavailable
	}
	if !ValidRevision(exactRevision) {
		return Profile{}, ErrInvalidRequest
	}
	tx, err := a.store.Begin(ctx)
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback(ctx)
	var profileID, revision, digest, status string
	var generation int32
	err = tx.QueryRow(ctx, `SELECT id,generation,revision,projection_digest,status FROM ci_repository_profile
		WHERE workspace_id=$1 AND project_id=$2 AND resource_id=$3 AND schema_version=$4`, scope.WorkspaceID, scope.ProjectID, scope.ResourceID, SchemaVersion).Scan(&profileID, &generation, &revision, &digest, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{Eligible: false, Reason: "absent"}, tx.Commit(ctx)
	}
	if err != nil {
		return Profile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, err
	}
	return profileResponse(profileID, "", generation, scope.Repository, revision, digest, status, exactRevision), nil
}

func (a *Aggregate) Enable(ctx context.Context, scope Scope, opaque []byte) (Profile, error) {
	profile, err := a.load(ctx, scope)
	if err != nil {
		return Profile{}, err
	}
	attestation, err := VerifyEnable(ctx, a.verifier, Evidence{ProfileID: profile.ProfileID, Generation: int(profile.Generation), Revision: profile.Revision, ProjectionDigest: profile.ProjectionDigest, OpaqueInput: opaque})
	if err != nil {
		return Profile{}, err
	}
	return a.transition(ctx, scope, profile, "enabled", attestation.Reference)
}

func (a *Aggregate) Disable(ctx context.Context, scope Scope) (Profile, error) {
	profile, err := a.load(ctx, scope)
	if err != nil {
		return Profile{}, err
	}
	return a.transition(ctx, scope, profile, "disabled", "")
}

// TombstoneResource runs before project_resource deletion. Retaining profile,
// receipt, and audit history prevents an orphaned resource from ever becoming
// discovery-eligible while still satisfying the repository's no-FK rule.
func (a *Aggregate) TombstoneResource(ctx context.Context, scope Scope) error {
	profile, err := a.load(ctx, scope)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = a.transition(ctx, scope, profile, "disabled", "")
	return err
}

func (a *Aggregate) load(ctx context.Context, scope Scope) (Profile, error) {
	if a == nil || a.store == nil {
		return Profile{}, ErrStoreUnavailable
	}
	tx, err := a.store.Begin(ctx)
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback(ctx)
	var id, revision, digest, status string
	var generation int32
	err = tx.QueryRow(ctx, `SELECT id,generation,revision,projection_digest,status FROM ci_repository_profile
		WHERE workspace_id=$1 AND project_id=$2 AND resource_id=$3 AND schema_version=$4`, scope.WorkspaceID, scope.ProjectID, scope.ResourceID, SchemaVersion).Scan(&id, &generation, &revision, &digest, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, err
	}
	return profileResponse(id, "", generation, scope.Repository, revision, digest, status, revision), nil
}

func (a *Aggregate) transition(ctx context.Context, scope Scope, previous Profile, status, reference string) (Profile, error) {
	tx, err := a.store.Begin(ctx)
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback(ctx)
	ct, err := tx.Exec(ctx, `UPDATE ci_repository_profile SET status=$2,adapter_attestation_reference=$3,updated_at=now()
		WHERE id=$1 AND generation=$4 AND revision=$5 AND projection_digest=$6`, previous.ProfileID, status, reference, previous.Generation, previous.Revision, previous.ProjectionDigest)
	if err != nil {
		return Profile{}, err
	}
	if ct.RowsAffected() != 1 {
		return Profile{}, ErrIdempotencyConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ci_repository_profile_audit
		(profile_id,workspace_id,project_id,resource_id,generation,action,source,actor_type,actor_id,projection_digest,attestation_reference)
		VALUES ($1,$2,$3,$4,$5,$6,'owner_admin','member',$7,$8,NULLIF($9,''))`,
		previous.ProfileID, scope.WorkspaceID, scope.ProjectID, scope.ResourceID, previous.Generation, map[string]string{"enabled": "enable", "disabled": "disable"}[status], scope.ActorID, previous.ProjectionDigest, reference,
	); err != nil {
		return Profile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, err
	}
	return profileResponse(previous.ProfileID, "", previous.Generation, scope.Repository, previous.Revision, previous.ProjectionDigest, status, previous.Revision), nil
}

func profileResponse(id, receiptID string, generation int32, repository, revision, digest, status, exactRevision string) Profile {
	reason := status
	eligible := status == "enabled" && revision == exactRevision
	if revision != exactRevision {
		reason = "revision_mismatch"
	} else if status != "enabled" {
		eligible = false
	}
	return Profile{ProfileID: id, ReceiptID: receiptID, Generation: generation, Repository: repository, Revision: revision, ProjectionDigest: digest, Status: status, Eligible: eligible, Reason: reason}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
