package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/ciprofile"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type ciRepositoryProfileResponse struct {
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

type enableCIRepositoryProfileRequest struct {
	AdapterAttestation json.RawMessage `json:"adapter_attestation"`
}

func (h *Handler) loadCIRepositoryProfileResource(w http.ResponseWriter, r *http.Request) (db.Project, db.ProjectResource, string, pgtype.UUID, bool) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return db.Project{}, db.ProjectResource{}, "", pgtype.UUID{}, false
	}
	if _, ok := h.requireWorkspaceRole(w, r, uuidToString(project.WorkspaceID), "project not found", "owner", "admin"); !ok {
		return db.Project{}, db.ProjectResource{}, "", pgtype.UUID{}, false
	}
	resourceID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "resourceId"), "resource id")
	if !ok {
		return db.Project{}, db.ProjectResource{}, "", pgtype.UUID{}, false
	}
	resource, err := h.Queries.GetProjectResourceInWorkspace(r.Context(), db.GetProjectResourceInWorkspaceParams{ID: resourceID, WorkspaceID: project.WorkspaceID})
	if err != nil || uuidToString(resource.ProjectID) != uuidToString(project.ID) || resource.ResourceType != "github_repo" {
		writeError(w, http.StatusNotFound, "github repository resource not found")
		return db.Project{}, db.ProjectResource{}, "", pgtype.UUID{}, false
	}
	var ref struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(resource.ResourceRef, &ref); err != nil {
		writeError(w, http.StatusBadRequest, "github repository resource is malformed")
		return db.Project{}, db.ProjectResource{}, "", pgtype.UUID{}, false
	}
	repository, err := ciprofile.CanonicalRepositoryIdentity(ref.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "github repository resource is malformed")
		return db.Project{}, db.ProjectResource{}, "", pgtype.UUID{}, false
	}
	return project, resource, repository, resourceID, true
}

// RegisterCIRepositoryProfile creates or rotates the native profile. The raw
// adapter input is never persisted; only a later verifier reference may be.
func (h *Handler) RegisterCIRepositoryProfile(w http.ResponseWriter, r *http.Request) {
	project, _, repository, resourceID, ok := h.loadCIRepositoryProfileResource(w, r)
	if !ok {
		return
	}
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actor, err := parseUUIDLoose(actorID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid actor")
		return
	}
	request, err := ciprofile.DecodeRegistration(readBoundedJSON(r, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if request.WorkspaceID != uuidToString(project.WorkspaceID) || request.ProjectID != uuidToString(project.ID) || request.ResourceID != uuidToString(resourceID) {
		writeError(w, http.StatusBadRequest, "request scope does not match project resource")
		return
	}
	digest, err := ciprofile.ProjectionDigest(repository, request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.TxStarter == nil {
		writeError(w, http.StatusServiceUnavailable, "ci repository-profile store unavailable")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin profile registration")
		return
	}
	defer tx.Rollback(r.Context())
	var receiptID, profileID pgtype.UUID
	var generation int32
	err = tx.QueryRow(r.Context(), `SELECT id, profile_id, generation FROM ci_repository_profile_receipt WHERE workspace_id=$1 AND project_id=$2 AND resource_id=$3 AND request_id=$4`, project.WorkspaceID, project.ID, resourceID, request.RequestID).Scan(&receiptID, &profileID, &generation)
	if err == nil {
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read registration receipt")
			return
		}
		writeJSON(w, http.StatusOK, ciRepositoryProfileResponse{ProfileID: uuidToString(profileID), Generation: generation, ReceiptID: uuidToString(receiptID), Repository: repository, Revision: request.Revision, ProjectionDigest: digest, Status: "pending_adapter", Eligible: false, Reason: "pending_adapter"})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to read registration receipt")
		return
	}
	var revision, existingDigest, status string
	err = tx.QueryRow(r.Context(), `SELECT id, generation, revision, projection_digest, status FROM ci_repository_profile WHERE workspace_id=$1 AND project_id=$2 AND resource_id=$3 AND schema_version=$4 FOR UPDATE`, project.WorkspaceID, project.ID, resourceID, ciprofile.SchemaVersion).Scan(&profileID, &generation, &revision, &existingDigest, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(r.Context(), `INSERT INTO ci_repository_profile (workspace_id, project_id, resource_id, schema_version, repository_identity, revision, workflow_class, workflow_version, job_class, check_name, service_classes, hosted_fallback, projection_digest, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,false,$12,$13) RETURNING id,generation`, project.WorkspaceID, project.ID, resourceID, ciprofile.SchemaVersion, repository, request.Revision, request.WorkflowClass, request.WorkflowVersion, request.JobClass, request.CheckName, `["postgresql_pgvector","redis"]`, digest, actor).Scan(&profileID, &generation)
		status = "pending_adapter"
	} else if err == nil && existingDigest != digest {
		err = tx.QueryRow(r.Context(), `UPDATE ci_repository_profile SET generation=generation+1, repository_identity=$2, revision=$3, projection_digest=$4, status='pending_adapter', adapter_attestation_reference=NULL, updated_at=now() WHERE id=$1 RETURNING generation`, profileID, repository, request.Revision, digest).Scan(&generation)
		status = "pending_adapter"
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to materialize profile")
		return
	}
	err = tx.QueryRow(r.Context(), `INSERT INTO ci_repository_profile_receipt (workspace_id, project_id, resource_id, request_id, profile_id, generation, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, project.WorkspaceID, project.ID, resourceID, request.RequestID, profileID, generation, actor).Scan(&receiptID)
	if err != nil {
		writeError(w, http.StatusConflict, "registration request already exists")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO ci_repository_profile_audit (profile_id, workspace_id, project_id, resource_id, generation, action, actor_type, actor_id, projection_digest) VALUES ($1,$2,$3,$4,$5,'register','member',$6,$7)`, profileID, project.WorkspaceID, project.ID, resourceID, generation, actor, digest)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to audit registration")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register profile")
		return
	}
	writeJSON(w, http.StatusCreated, ciRepositoryProfileResponse{ProfileID: uuidToString(profileID), Generation: generation, ReceiptID: uuidToString(receiptID), Repository: repository, Revision: request.Revision, ProjectionDigest: digest, Status: status, Eligible: false, Reason: "pending_adapter"})
}

func (h *Handler) GetCIRepositoryProfileDiscovery(w http.ResponseWriter, r *http.Request) {
	project, _, repository, resourceID, ok := h.loadCIRepositoryProfileResource(w, r)
	if !ok {
		return
	}
	revision := r.URL.Query().Get("revision")
	if !ciprofile.ValidRevision(revision) {
		writeError(w, http.StatusBadRequest, "revision must be 40 lowercase hexadecimal characters")
		return
	}
	var profileID pgtype.UUID
	var generation int32
	var storedRevision, digest, status string
	err := h.DB.QueryRow(r.Context(), `SELECT id,generation,revision,projection_digest,status FROM ci_repository_profile WHERE workspace_id=$1 AND project_id=$2 AND resource_id=$3 AND schema_version=$4`, project.WorkspaceID, project.ID, resourceID, ciprofile.SchemaVersion).Scan(&profileID, &generation, &storedRevision, &digest, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, ciRepositoryProfileResponse{Eligible: false, Reason: "absent"})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to discover profile")
		return
	}
	resp := ciRepositoryProfileResponse{ProfileID: uuidToString(profileID), Generation: generation, Repository: repository, Revision: storedRevision, ProjectionDigest: digest, Status: status, Eligible: status == "enabled" && storedRevision == revision, Reason: "enabled"}
	if storedRevision != revision {
		resp.Eligible = false
		resp.Reason = "revision_mismatch"
	} else if status == "pending_adapter" {
		resp.Reason = "pending_adapter"
	} else if status == "disabled" {
		resp.Reason = "disabled"
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) EnableCIRepositoryProfile(w http.ResponseWriter, r *http.Request) {
	project, _, _, resourceID, ok := h.loadCIRepositoryProfileResource(w, r)
	if !ok {
		return
	}
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actor, err := parseUUIDLoose(actorID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid actor")
		return
	}
	var req enableCIRepositoryProfileRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || len(req.AdapterAttestation) == 0 || !json.Valid(req.AdapterAttestation) {
		writeError(w, http.StatusBadRequest, "adapter_attestation is required")
		return
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request must contain one JSON object")
		return
	}
	var profileID pgtype.UUID
	var generation int32
	var revision, digest string
	err = h.DB.QueryRow(r.Context(), `SELECT id,generation,revision,projection_digest FROM ci_repository_profile WHERE workspace_id=$1 AND project_id=$2 AND resource_id=$3 AND schema_version=$4`, project.WorkspaceID, project.ID, resourceID, ciprofile.SchemaVersion).Scan(&profileID, &generation, &revision, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "ci repository profile not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load profile")
		return
	}
	attestation, err := ciprofile.VerifyEnable(r.Context(), h.CIProfileVerifier, ciprofile.Evidence{ProfileID: uuidToString(profileID), Generation: int(generation), Revision: revision, ProjectionDigest: digest, OpaqueInput: req.AdapterAttestation})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if h.TxStarter == nil {
		writeError(w, http.StatusServiceUnavailable, "ci repository-profile store unavailable")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin enable")
		return
	}
	defer tx.Rollback(r.Context())
	ct, err := tx.Exec(r.Context(), `UPDATE ci_repository_profile SET status='enabled',adapter_attestation_reference=$2,updated_at=now() WHERE id=$1 AND generation=$3 AND revision=$4 AND projection_digest=$5`, profileID, attestation.Reference, generation, revision, digest)
	if err != nil || ct.RowsAffected() != 1 {
		writeError(w, http.StatusConflict, "profile changed before verification")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO ci_repository_profile_audit (profile_id,workspace_id,project_id,resource_id,generation,action,actor_type,actor_id,projection_digest,attestation_reference) VALUES ($1,$2,$3,$4,$5,'enable','member',$6,$7,$8)`, profileID, project.WorkspaceID, project.ID, resourceID, generation, actor, digest, attestation.Reference)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to audit enable")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enable profile")
		return
	}
	writeJSON(w, http.StatusOK, ciRepositoryProfileResponse{ProfileID: uuidToString(profileID), Generation: generation, Revision: revision, ProjectionDigest: digest, Status: "enabled", Eligible: true, Reason: "enabled"})
}

func (h *Handler) DisableCIRepositoryProfile(w http.ResponseWriter, r *http.Request) {
	project, _, _, resourceID, ok := h.loadCIRepositoryProfileResource(w, r)
	if !ok {
		return
	}
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actor, err := parseUUIDLoose(actorID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid actor")
		return
	}
	if h.TxStarter == nil {
		writeError(w, http.StatusServiceUnavailable, "ci repository-profile store unavailable")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin disable")
		return
	}
	defer tx.Rollback(r.Context())
	var profileID pgtype.UUID
	var generation int32
	var revision, digest string
	err = tx.QueryRow(r.Context(), `UPDATE ci_repository_profile SET status='disabled',updated_at=now() WHERE workspace_id=$1 AND project_id=$2 AND resource_id=$3 AND schema_version=$4 RETURNING id,generation,revision,projection_digest`, project.WorkspaceID, project.ID, resourceID, ciprofile.SchemaVersion).Scan(&profileID, &generation, &revision, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "ci repository profile not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to disable profile")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO ci_repository_profile_audit (profile_id,workspace_id,project_id,resource_id,generation,action,actor_type,actor_id,projection_digest) VALUES ($1,$2,$3,$4,$5,'disable','member',$6,$7)`, profileID, project.WorkspaceID, project.ID, resourceID, generation, actor, digest)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to audit disable")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to disable profile")
		return
	}
	writeJSON(w, http.StatusOK, ciRepositoryProfileResponse{ProfileID: uuidToString(profileID), Generation: generation, Revision: revision, ProjectionDigest: digest, Status: "disabled", Eligible: false, Reason: "disabled"})
}

func readBoundedJSON(r *http.Request, limit int64) []byte {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil
	}
	return body
}
