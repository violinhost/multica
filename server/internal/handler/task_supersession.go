package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type RecordTaskSupersessionReceiptRequest struct {
	SupersededByTaskID   string   `json:"superseded_by_task_id"`
	SupersededCommentIDs []string `json:"superseded_comment_ids"`
}

func normalizeDistinctUUIDs(ids []pgtype.UUID) []pgtype.UUID {
	if len(ids) == 0 {
		return nil
	}
	byID := make(map[string]pgtype.UUID, len(ids))
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		if !id.Valid {
			continue
		}
		key := uuidToString(id)
		if _, exists := byID[key]; exists {
			continue
		}
		byID[key] = id
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]pgtype.UUID, 0, len(keys))
	for _, key := range keys {
		out = append(out, byID[key])
	}
	return out
}

func normalizedUUIDStrings(ids []pgtype.UUID) []string {
	normalized := normalizeDistinctUUIDs(ids)
	out := make([]string, 0, len(normalized))
	for _, id := range normalized {
		out = append(out, uuidToString(id))
	}
	return out
}

func plannedCommentIDsForTask(task db.AgentTaskQueue) []pgtype.UUID {
	ids := make([]pgtype.UUID, 0, len(task.CoalescedCommentIds)+1)
	ids = append(ids, task.CoalescedCommentIds...)
	if task.TriggerCommentID.Valid {
		ids = append(ids, task.TriggerCommentID)
	}
	return normalizeDistinctUUIDs(ids)
}

// RecordTaskSupersessionReceipt lets a workspace owner/admin stamp a trusted
// explicit supersession receipt onto a queued comment task. The claim path
// revalidates this receipt before consuming it.
func (h *Handler) RecordTaskSupersessionReceipt(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	taskUUID, ok := parseUUIDOrBadRequest(w, taskID, "task id")
	if !ok {
		return
	}

	task, err := h.Queries.GetAgentTask(r.Context(), taskUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	workspaceID := h.TaskService.ResolveTaskWorkspaceID(r.Context(), task)
	if workspaceID == "" {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "task not found", "owner", "admin"); !ok {
		return
	}
	workspaceUUID := parseUUID(workspaceID)

	queuedTask, err := h.Queries.GetAgentTaskInWorkspace(r.Context(), db.GetAgentTaskInWorkspaceParams{
		ID:          taskUUID,
		WorkspaceID: workspaceUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if queuedTask.Status != "queued" || (!queuedTask.TriggerCommentID.Valid && len(queuedTask.CoalescedCommentIds) == 0) {
		writeError(w, http.StatusBadRequest, "task must be a queued comment task")
		return
	}

	var req RecordTaskSupersessionReceiptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	supersededByTaskUUID, ok := parseUUIDOrBadRequest(w, req.SupersededByTaskID, "superseded_by_task_id")
	if !ok {
		return
	}
	supersededCommentIDs, ok := parseUUIDSliceOrBadRequest(w, req.SupersededCommentIDs, "superseded_comment_ids")
	if !ok {
		return
	}

	supersedingTask, err := h.Queries.GetAgentTaskInWorkspace(r.Context(), db.GetAgentTaskInWorkspaceParams{
		ID:          supersededByTaskUUID,
		WorkspaceID: workspaceUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "superseding task not found")
		return
	}
	if supersedingTask.Status != "completed" || !supersedingTask.CompletedAt.Valid {
		writeError(w, http.StatusBadRequest, "superseding task must be completed")
		return
	}
	if !queuedTask.IssueID.Valid || !supersedingTask.IssueID.Valid ||
		uuidToString(queuedTask.IssueID) != uuidToString(supersedingTask.IssueID) ||
		uuidToString(queuedTask.AgentID) != uuidToString(supersedingTask.AgentID) {
		writeError(w, http.StatusBadRequest, "superseding task must belong to the same issue and agent")
		return
	}

	plannedCommentIDs := plannedCommentIDsForTask(queuedTask)
	if len(plannedCommentIDs) == 0 {
		writeError(w, http.StatusBadRequest, "task must be a queued comment task")
		return
	}
	normalizedRequested := normalizeDistinctUUIDs(supersededCommentIDs)
	if !slicesEqual(normalizedUUIDStrings(plannedCommentIDs), normalizedUUIDStrings(normalizedRequested)) {
		writeError(w, http.StatusBadRequest, "superseded_comment_ids must exactly match the queued task plan")
		return
	}

	updated, err := h.Queries.RecordTrustedTaskSupersessionReceipt(r.Context(), db.RecordTrustedTaskSupersessionReceiptParams{
		SupersededByTaskID:   supersededByTaskUUID,
		SupersededCommentIds: normalizedRequested,
		TaskID:               taskUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "task must still be queued")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to record supersession receipt")
		return
	}

	resp := taskToResponse(updated, workspaceID)
	h.hydrateTaskAttributions(r.Context(), []*TaskAttribution{resp.Attribution})
	writeJSON(w, http.StatusOK, resp)
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
