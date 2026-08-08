package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/managedaction"
)

// ListManagedActionCapabilities exposes only the fixed managed-action specs
// enabled for a project. It is deliberately project-scoped: clients must not
// infer an action is permitted from a global catalog entry.
func (h *Handler) ListManagedActionCapabilities(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	ws, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "projectId"), "project_id")
	if !ok {
		return
	}
	caps, err := h.ManagedActionService.Capabilities(r.Context(), ws, projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found in this workspace")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": caps})
}

// StartManagedAction accepts the narrow W1 native request. The service owns
// all authorization and durable mutation; this transport only binds the
// authenticated workspace and actor to the supplied payload.
func (h *Handler) StartManagedAction(w http.ResponseWriter, r *http.Request) {
	var req managedaction.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	if req.WorkspaceID != workspaceID {
		writeError(w, http.StatusBadRequest, "workspace_id does not match request workspace")
		return
	}
	actorID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if req.Actor != actorID {
		writeError(w, http.StatusBadRequest, "actor does not match authenticated user")
		return
	}
	receipt, err := h.ManagedActionService.Start(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, managedaction.ErrAuthorityUnavailable):
			writeError(w, http.StatusServiceUnavailable, "managed action authority verifier is unavailable")
		case errors.Is(err, managedaction.ErrAuthorityInvalid), errors.Is(err, managedaction.ErrInvalidRequest), errors.Is(err, managedaction.ErrUnknownAction), errors.Is(err, managedaction.ErrActionDisabled):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to start managed action")
		}
		return
	}
	writeJSON(w, http.StatusCreated, receipt)
}
