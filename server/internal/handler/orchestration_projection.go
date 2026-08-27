package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/plugincontract"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const orchestrationProjectionSchemaV1 = 1

type OrchestrationProjectionResponse struct {
	SchemaVersion   int32                             `json:"schema_version"`
	Producer        string                            `json:"producer"`
	ReceiptID       string                            `json:"receipt_id"`
	ReceiptDigest   string                            `json:"receipt_digest"`
	WorkflowID      string                            `json:"workflow_id"`
	Stage           string                            `json:"stage"`
	Role            string                            `json:"role"`
	Substate        string                            `json:"substate"`
	ReasonCode      string                            `json:"reason_code"`
	Since           string                            `json:"since"`
	ElapsedSeconds  int64                             `json:"elapsed_seconds"`
	SlaPosture      string                            `json:"sla_posture"`
	RouteGeneration int64                             `json:"route_generation"`
	NativeStatus    OrchestrationNativeStatusResponse `json:"native_status"`
	NextAction      OrchestrationNextActionResponse   `json:"next_action"`
}
type OrchestrationNativeStatusResponse struct {
	Key          string `json:"key"`
	Category     string `json:"category"`
	DefinitionID string `json:"definition_id"`
}
type OrchestrationNextActionResponse struct {
	Code   string  `json:"code"`
	Target *string `json:"target,omitempty"`
}

type orchestrationProjectionRequest struct {
	SchemaVersion   int32  `json:"schema_version"`
	ReceiptID       string `json:"receipt_id"`
	ReceiptDigest   string `json:"receipt_digest"`
	WorkflowID      string `json:"workflow_id"`
	Stage           string `json:"stage"`
	Role            string `json:"role"`
	Substate        string `json:"substate"`
	ReasonCode      string `json:"reason_code"`
	Since           string `json:"since"`
	ElapsedSeconds  int64  `json:"elapsed_seconds"`
	SlaPosture      string `json:"sla_posture"`
	RouteGeneration int64  `json:"route_generation"`
	NativeStatus    struct {
		Key          string `json:"key"`
		Category     string `json:"category"`
		DefinitionID string `json:"definition_id"`
	} `json:"native_status"`
	NextAction struct {
		Code   string  `json:"code"`
		Target *string `json:"target"`
	} `json:"next_action"`
	ExpectedIssueRevision int64 `json:"expected_issue_revision"`
}

func orchestrationProjectionResponse(p db.IssueOrchestrationProjection) *OrchestrationProjectionResponse {
	if p.SchemaVersion != orchestrationProjectionSchemaV1 {
		return nil
	}
	return &OrchestrationProjectionResponse{SchemaVersion: p.SchemaVersion, Producer: p.Producer, ReceiptID: p.ReceiptID, ReceiptDigest: p.ReceiptDigest, WorkflowID: p.WorkflowID, Stage: p.Stage, Role: p.Role, Substate: p.Substate, ReasonCode: p.ReasonCode, Since: timestampToString(p.Since), ElapsedSeconds: p.ElapsedSeconds, SlaPosture: p.SlaPosture, RouteGeneration: p.RouteGeneration, NativeStatus: OrchestrationNativeStatusResponse{Key: p.NativeStatusKey, Category: p.NativeStatusCategory, DefinitionID: uuidToString(p.NativeStatusDefinitionID)}, NextAction: OrchestrationNextActionResponse{Code: p.NextActionCode, Target: textToPtr(p.NextActionTarget)}}
}

func (h *Handler) fillOrchestrationProjection(ctx context.Context, issue db.Issue, resp *IssueResponse) {
	p, err := h.Queries.GetIssueOrchestrationProjection(ctx, db.GetIssueOrchestrationProjectionParams{IssueID: issue.ID, WorkspaceID: issue.WorkspaceID})
	if err == nil {
		resp.OrchestrationProjection = orchestrationProjectionResponse(p)
	}
}

// fillOrchestrationProjections hydrates list payloads in one query. A malformed
// or future stored schema is omitted, preserving ordinary issue reads.
func (h *Handler) fillOrchestrationProjections(ctx context.Context, workspaceID pgtype.UUID, issueIDs []pgtype.UUID, resps []IssueResponse) {
	if len(issueIDs) == 0 {
		return
	}
	projections, err := h.Queries.ListIssueOrchestrationProjections(ctx, db.ListIssueOrchestrationProjectionsParams{WorkspaceID: workspaceID, IssueIds: issueIDs})
	if err != nil {
		return
	}
	byIssue := make(map[string]*OrchestrationProjectionResponse, len(projections))
	for _, projection := range projections {
		if response := orchestrationProjectionResponse(projection); response != nil {
			byIssue[uuidToString(projection.IssueID)] = response
		}
	}
	for i := range resps {
		resps[i].OrchestrationProjection = byIssue[resps[i].ID]
	}
}

// UpsertPluginOrchestrationProjection is intentionally isolated from generic
// issue PATCH: it only records a receipt-bound read projection and never changes
// native issue.status.
func (h *Handler) UpsertPluginOrchestrationProjection(w http.ResponseWriter, r *http.Request) {
	if !featureflags.AutomulticaOrchestrationProjectionEnabled(r.Context(), h.FeatureFlags) {
		writeError(w, http.StatusServiceUnavailable, "Automultica orchestration projection is not enabled")
		return
	}
	caller, _, ok := h.pluginCaller(w, r, plugincontract.ScopeAutomulticaProjectionWrite)
	if !ok {
		return
	}
	// This is a deployment-owned capability marker on the installation, not a
	// request assertion. A different plugin granted the narrow scope cannot opt
	// itself into becoming the Automultica receipt producer.
	var config map[string]any
	if json.Unmarshal(caller.Installation.Config, &config) != nil || config["automultica_projection_producer"] != true {
		writeError(w, http.StatusForbidden, "this Plugin installation is not configured as the Automultica projection producer")
		return
	}
	var req orchestrationProjectionRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid orchestration projection request")
		return
	}
	if req.SchemaVersion != orchestrationProjectionSchemaV1 || req.ReceiptID == "" || req.ReceiptDigest == "" || req.WorkflowID == "" || req.Stage == "" || req.Role == "" || req.Substate == "" || req.ReasonCode == "" || req.NextAction.Code == "" || req.ExpectedIssueRevision < 1 || req.RouteGeneration < 1 || req.ElapsedSeconds < 0 {
		writeError(w, http.StatusBadRequest, "invalid orchestration projection fields")
		return
	}
	if req.SlaPosture != "within_sla" && req.SlaPosture != "at_risk" && req.SlaPosture != "breached" && req.SlaPosture != "unknown" {
		writeError(w, http.StatusBadRequest, "invalid sla_posture")
		return
	}
	since, err := time.Parse(time.RFC3339, req.Since)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid since")
		return
	}
	definitionID, ok := parseUUIDOrBadRequest(w, req.NativeStatus.DefinitionID, "native_status.definition_id")
	if !ok {
		return
	}
	issue, ok := h.pluginIssueForUser(w, r, caller, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin projection update")
		return
	}
	defer tx.Rollback(r.Context())
	q := h.Queries.WithTx(tx)
	locked, err := q.LockIssueForOrchestrationProjection(r.Context(), db.LockIssueForOrchestrationProjectionParams{ID: issue.ID, WorkspaceID: caller.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if locked.Revision != req.ExpectedIssueRevision {
		writeRevisionConflict(w, "issue", locked.ID, req.ExpectedIssueRevision, locked.Revision)
		return
	}
	status, err := q.GetIssueStatusEntryByID(r.Context(), db.GetIssueStatusEntryByIDParams{ID: definitionID, WorkspaceID: caller.WorkspaceID})
	if err != nil || status.ArchivedAt.Valid || status.Key != locked.Status || status.Key != req.NativeStatus.Key || status.Category != req.NativeStatus.Category {
		writeError(w, http.StatusBadRequest, "native status snapshot does not match the current status catalog")
		return
	}
	if existing, err := q.GetIssueOrchestrationProjection(r.Context(), db.GetIssueOrchestrationProjectionParams{IssueID: locked.ID, WorkspaceID: caller.WorkspaceID}); err == nil {
		if req.RouteGeneration < existing.RouteGeneration || (req.RouteGeneration == existing.RouteGeneration && (req.ReceiptID != existing.ReceiptID || req.ReceiptDigest != existing.ReceiptDigest)) {
			writeError(w, http.StatusConflict, "route_generation conflicts with the accepted projection")
			return
		}
		if req.RouteGeneration == existing.RouteGeneration {
			resp := issueToResponse(locked, h.getIssuePrefix(r.Context(), locked.WorkspaceID))
			resp.OrchestrationProjection = orchestrationProjectionResponse(existing)
			writeJSON(w, http.StatusOK, resp)
			return
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to read projection")
		return
	}
	updated, err := q.TouchIssueForOrchestrationProjection(r.Context(), db.TouchIssueForOrchestrationProjectionParams{ID: locked.ID, WorkspaceID: caller.WorkspaceID, ExpectedRevision: req.ExpectedIssueRevision})
	if err != nil {
		writeRevisionConflict(w, "issue", locked.ID, req.ExpectedIssueRevision, locked.Revision)
		return
	}
	p, err := q.UpsertIssueOrchestrationProjection(r.Context(), db.UpsertIssueOrchestrationProjectionParams{IssueID: locked.ID, WorkspaceID: caller.WorkspaceID, SchemaVersion: req.SchemaVersion, ProducerInstallationID: caller.Installation.ID, ReceiptID: req.ReceiptID, ReceiptDigest: req.ReceiptDigest, WorkflowID: req.WorkflowID, Stage: req.Stage, Role: req.Role, Substate: req.Substate, ReasonCode: req.ReasonCode, Since: pgtype.Timestamptz{Time: since, Valid: true}, ElapsedSeconds: req.ElapsedSeconds, SlaPosture: req.SlaPosture, RouteGeneration: req.RouteGeneration, NativeStatusKey: req.NativeStatus.Key, NativeStatusCategory: req.NativeStatus.Category, NativeStatusDefinitionID: definitionID, NextActionCode: req.NextAction.Code, NextActionTarget: util.PtrToText(req.NextAction.Target), IssueRevision: updated.Revision})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist projection")
		return
	}
	if _, err = q.CreateOrchestrationProjectionReceipt(r.Context(), db.CreateOrchestrationProjectionReceiptParams{ProducerInstallationID: caller.Installation.ID, ReceiptID: req.ReceiptID, ReceiptDigest: req.ReceiptDigest, IssueID: locked.ID, RouteGeneration: req.RouteGeneration}); err != nil {
		writeError(w, http.StatusConflict, "receipt already conflicts with another projection")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit projection update")
		return
	}
	resp := issueToResponse(updated, h.getIssuePrefix(r.Context(), updated.WorkspaceID))
	h.fillStatusCategory(r.Context(), updated.WorkspaceID, &resp)
	resp.OrchestrationProjection = orchestrationProjectionResponse(p)
	h.publish(protocol.EventIssueUpdated, uuidToString(updated.WorkspaceID), "plugin", uuidToString(caller.Installation.ID), map[string]any{"issue": resp, "orchestration_projection_changed": true})
	writeJSON(w, http.StatusOK, resp)
}
