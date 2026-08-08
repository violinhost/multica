package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/ciprofile"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// loadCIRepositoryProfileResource is the HTTP ownership boundary. The
// aggregate receives only server-derived repository identity and UUID scope.
func (h *Handler) loadCIRepositoryProfileResource(w http.ResponseWriter, r *http.Request, write bool) (db.Project, db.ProjectResource, ciprofile.Scope, bool) {
	project, ok := h.loadProjectForResource(w, r, chi.URLParam(r, "id"))
	if !ok {
		return db.Project{}, db.ProjectResource{}, ciprofile.Scope{}, false
	}
	roles := []string{"owner", "admin", "member"}
	if write {
		roles = []string{"owner", "admin"}
	}
	if _, ok := h.requireWorkspaceRole(w, r, uuidToString(project.WorkspaceID), "project not found", roles...); !ok {
		return db.Project{}, db.ProjectResource{}, ciprofile.Scope{}, false
	}
	resourceID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "resourceId"), "resource id")
	if !ok {
		return db.Project{}, db.ProjectResource{}, ciprofile.Scope{}, false
	}
	resource, err := h.Queries.GetProjectResourceInWorkspace(r.Context(), db.GetProjectResourceInWorkspaceParams{ID: resourceID, WorkspaceID: project.WorkspaceID})
	if err != nil || uuidToString(resource.ProjectID) != uuidToString(project.ID) || resource.ResourceType != "github_repo" {
		writeError(w, http.StatusNotFound, "github repository resource not found")
		return db.Project{}, db.ProjectResource{}, ciprofile.Scope{}, false
	}
	var ref struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(resource.ResourceRef, &ref); err != nil {
		writeError(w, http.StatusBadRequest, "github repository resource is malformed")
		return db.Project{}, db.ProjectResource{}, ciprofile.Scope{}, false
	}
	repository, err := ciprofile.CanonicalRepositoryIdentity(ref.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "github repository resource is malformed")
		return db.Project{}, db.ProjectResource{}, ciprofile.Scope{}, false
	}
	scope := ciprofile.Scope{WorkspaceID: project.WorkspaceID, ProjectID: project.ID, ResourceID: resourceID, Repository: repository}
	if write {
		actorID, ok := requireUserID(w, r)
		if !ok {
			return db.Project{}, db.ProjectResource{}, ciprofile.Scope{}, false
		}
		actor, err := parseUUIDLoose(actorID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid actor")
			return db.Project{}, db.ProjectResource{}, ciprofile.Scope{}, false
		}
		scope.ActorID = actor
	}
	return project, resource, scope, true
}

func (h *Handler) ciProfileAggregate() *ciprofile.Aggregate {
	return ciprofile.NewAggregate(h.TxStarter, h.CIProfileVerifier)
}

func writeCIProfileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ciprofile.ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ciprofile.ErrNotFound):
		writeError(w, http.StatusNotFound, "ci repository profile not found")
	case errors.Is(err, ciprofile.ErrStoreUnavailable), errors.Is(err, ciprofile.ErrVerifierUnavailable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, ciprofile.ErrIdempotencyConflict), errors.Is(err, ciprofile.ErrInvalidAttestation), errors.Is(err, ciprofile.ErrRepositoryMismatch):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "ci repository-profile operation failed")
	}
}

func (h *Handler) RegisterCIRepositoryProfile(w http.ResponseWriter, r *http.Request) {
	_, _, scope, ok := h.loadCIRepositoryProfileResource(w, r, true)
	if !ok {
		return
	}
	request, err := ciprofile.DecodeRegistration(readBoundedJSON(r, 64<<10))
	if err != nil {
		writeCIProfileError(w, err)
		return
	}
	if request.WorkspaceID != uuidToString(scope.WorkspaceID) || request.ProjectID != uuidToString(scope.ProjectID) || request.ResourceID != uuidToString(scope.ResourceID) {
		writeError(w, http.StatusBadRequest, "request scope does not match project resource")
		return
	}
	profile, created, err := h.ciProfileAggregate().Register(r.Context(), scope, request)
	if err != nil {
		writeCIProfileError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, profile)
}

// GetCIRepositoryProfileDiscovery is intentionally usable by ordinary
// workspace members. Its response is a sanitized candidate only; the service
// never returns raw attestation, routing, credential, or capacity data.
func (h *Handler) GetCIRepositoryProfileDiscovery(w http.ResponseWriter, r *http.Request) {
	_, _, scope, ok := h.loadCIRepositoryProfileResource(w, r, false)
	if !ok {
		return
	}
	profile, err := h.ciProfileAggregate().Discover(r.Context(), scope, r.URL.Query().Get("revision"))
	if err != nil {
		writeCIProfileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) EnableCIRepositoryProfile(w http.ResponseWriter, r *http.Request) {
	_, _, scope, ok := h.loadCIRepositoryProfileResource(w, r, true)
	if !ok {
		return
	}
	attestation, err := ciprofile.DecodeEnableAttestation(readBoundedJSON(r, 64<<10))
	if err != nil {
		writeCIProfileError(w, err)
		return
	}
	profile, err := h.ciProfileAggregate().Enable(r.Context(), scope, attestation)
	if err != nil {
		writeCIProfileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) DisableCIRepositoryProfile(w http.ResponseWriter, r *http.Request) {
	_, _, scope, ok := h.loadCIRepositoryProfileResource(w, r, true)
	if !ok {
		return
	}
	if err := ciprofile.DecodeDisableRequest(readBoundedJSON(r, 64<<10)); err != nil {
		writeCIProfileError(w, err)
		return
	}
	profile, err := h.ciProfileAggregate().Disable(r.Context(), scope)
	if err != nil {
		writeCIProfileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func readBoundedJSON(r *http.Request, limit int64) []byte {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil
	}
	return body
}

// ciProfileScopeForResource is deliberately separate from the endpoint loader:
// resource update and deletion already have a resolved resource row.
func ciProfileScopeForResource(project db.Project, resource db.ProjectResource, actor pgtype.UUID) (ciprofile.Scope, error) {
	var ref struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(resource.ResourceRef, &ref); err != nil {
		return ciprofile.Scope{}, err
	}
	repository, err := ciprofile.CanonicalRepositoryIdentity(ref.URL)
	if err != nil {
		return ciprofile.Scope{}, err
	}
	return ciprofile.Scope{WorkspaceID: project.WorkspaceID, ProjectID: project.ID, ResourceID: resource.ID, Repository: repository, ActorID: actor}, nil
}
