package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func projectionIngressFixture(t *testing.T) (issueID, installationID string, request func(map[string]any) *http.Request) {
	t.Helper()
	withPluginsV1Flag(t, testHandler, true)
	withAutomulticaOrchestrationProjectionFlag(t, testHandler, true)
	var projectionTablesReady bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT to_regclass('plugin_installation') IS NOT NULL
		   AND to_regclass('issue_orchestration_projection') IS NOT NULL
		   AND to_regclass('issue_orchestration_projection_receipt') IS NOT NULL`).Scan(&projectionTablesReady); err != nil {
		t.Fatalf("check projection test schema: %v", err)
	}
	if !projectionTablesReady {
		t.Skip("orchestration projection migrations are not applied")
	}

	installationID = dbfx.Insert(t, "plugin_installation", testutil.Cols{
		"workspace_id":   testWorkspaceID,
		"plugin_key":     "com.example.projection." + t.Name(),
		"source_url":     "local:projection-test",
		"version":        "1.0.0",
		"manifest":       []byte(`{"manifest_version":1}`),
		"granted_scopes": []byte(`["automultica:projection:write"]`),
		"installed_by":   testUserID,
		"config":         []byte(`{"automultica_projection_producer":true}`),
	})
	// The fixture's boring todo issue gives this test a real catalog status whose
	// id, key, and category the handler validates together.
	issueID = dbfx.Issue(t, "projection ingress")

	request = func(body map[string]any) *http.Request {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal projection request: %v", err)
		}
		req := pluginHandlerRequest(http.MethodPut, "/api/issues/"+issueID+"/orchestration-projection", raw, map[string]string{"id": issueID})
		req.Header.Set(pluginInstallationHeader, installationID)
		return req
	}
	return issueID, installationID, request
}

func projectionRequest(t *testing.T, issueID, receiptID, receiptDigest string, routeGeneration, expectedRevision int64) map[string]any {
	t.Helper()
	var statusID, statusKey, statusCategory string
	dbfx.QueryRow(t, `
		SELECT s.id, s.key, s.category
		FROM issue i
		JOIN issue_status s ON s.workspace_id = i.workspace_id AND s.key = i.status
		WHERE i.id = $1`, issueID).Scan(&statusID, &statusKey, &statusCategory)

	return map[string]any{
		"schema_version":          1,
		"receipt_id":              receiptID,
		"receipt_digest":          receiptDigest,
		"workflow_id":             "workflow-1",
		"stage":                   "executing",
		"role":                    "worker",
		"substate":                "running",
		"reason_code":             "work_in_progress",
		"since":                   "2026-08-27T12:00:00Z",
		"elapsed_seconds":         30,
		"sla_posture":             "within_sla",
		"route_generation":        routeGeneration,
		"expected_issue_revision": expectedRevision,
		"native_status": map[string]any{
			"id":            statusID,
			"definition_id": statusID,
			"key":           statusKey,
			"category":      statusCategory,
		},
		"next_action": map[string]any{"code": "wait"},
	}
}

func issueRevisionAndStatus(t *testing.T, issueID string) (int64, string) {
	t.Helper()
	var revision int64
	var status string
	dbfx.QueryRow(t, `SELECT revision, status FROM issue WHERE id = $1`, issueID).Scan(&revision, &status)
	return revision, status
}

func TestOrchestrationProjectionIngressIsDefaultOff(t *testing.T) {
	recorder := httptest.NewRecorder()
	testHandler.UpsertPluginOrchestrationProjection(recorder, pluginActionRequest(
		http.MethodPut,
		"/issues/not-an-issue/orchestration-projection",
		"",
		json.RawMessage(`{}`),
		map[string]string{"id": "not-an-issue"},
	))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("default-off ingress status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestUpsertPluginOrchestrationProjectionIngressGuards(t *testing.T) {
	t.Run("requires configured producer", func(t *testing.T) {
		issueID, installationID, request := projectionIngressFixture(t)
		dbfx.Exec(t, `UPDATE plugin_installation SET config = '{}'::jsonb WHERE id = $1`, installationID)
		revision, _ := issueRevisionAndStatus(t, issueID)

		recorder := httptest.NewRecorder()
		testHandler.UpsertPluginOrchestrationProjection(recorder, request(projectionRequest(t, issueID, "receipt-unconfigured", "digest-unconfigured", 1, revision)))
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s, want 403", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("rejects stale revision without projection", func(t *testing.T) {
		issueID, _, request := projectionIngressFixture(t)
		revision, status := issueRevisionAndStatus(t, issueID)

		recorder := httptest.NewRecorder()
		testHandler.UpsertPluginOrchestrationProjection(recorder, request(projectionRequest(t, issueID, "receipt-stale", "digest-stale", 1, revision+1)))
		if recorder.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%s, want 409", recorder.Code, recorder.Body.String())
		}
		gotRevision, gotStatus := issueRevisionAndStatus(t, issueID)
		if gotRevision != revision || gotStatus != status {
			t.Fatalf("stale write changed issue: revision=%d status=%q; want revision=%d status=%q", gotRevision, gotStatus, revision, status)
		}
		if count := dbfx.Count(t, `SELECT count(*) FROM issue_orchestration_projection WHERE issue_id = $1`, issueID); count != 0 {
			t.Fatalf("stale write persisted %d projections", count)
		}
	})
}

func TestUpsertPluginOrchestrationProjectionPersistsReceiptAndGuardsGeneration(t *testing.T) {
	issueID, installationID, request := projectionIngressFixture(t)
	dbfx.Cleanup(t, `DELETE FROM issue_orchestration_projection_receipt WHERE issue_id = $1`, issueID)
	dbfx.Cleanup(t, `DELETE FROM issue_orchestration_projection WHERE issue_id = $1`, issueID)

	revision, originalStatus := issueRevisionAndStatus(t, issueID)
	first := projectionRequest(t, issueID, "receipt-1", "digest-1", 1, revision)
	recorder := httptest.NewRecorder()
	testHandler.UpsertPluginOrchestrationProjection(recorder, request(first))
	if recorder.Code != http.StatusOK {
		t.Fatalf("first write: status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	updatedRevision, updatedStatus := issueRevisionAndStatus(t, issueID)
	if updatedRevision != revision+1 {
		t.Fatalf("revision=%d, want %d", updatedRevision, revision+1)
	}
	if updatedStatus != originalStatus {
		t.Fatalf("projection changed native status to %q, want %q", updatedStatus, originalStatus)
	}
	var receiptCount int
	dbfx.QueryRow(t, `SELECT count(*) FROM issue_orchestration_projection_receipt WHERE producer_installation_id = $1 AND receipt_id = $2 AND receipt_digest = $3 AND issue_id = $4 AND route_generation = $5`, installationID, "receipt-1", "digest-1", issueID, 1).Scan(&receiptCount)
	if receiptCount != 1 {
		t.Fatalf("receipt count=%d, want 1", receiptCount)
	}

	conflict := projectionRequest(t, issueID, "receipt-other", "digest-other", 1, updatedRevision)
	recorder = httptest.NewRecorder()
	testHandler.UpsertPluginOrchestrationProjection(recorder, request(conflict))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("same generation with different receipt: status=%d body=%s, want 409", recorder.Code, recorder.Body.String())
	}
	finalRevision, finalStatus := issueRevisionAndStatus(t, issueID)
	if finalRevision != updatedRevision || finalStatus != originalStatus {
		t.Fatalf("generation conflict changed issue: revision=%d status=%q; want revision=%d status=%q", finalRevision, finalStatus, updatedRevision, originalStatus)
	}
	if count := dbfx.Count(t, `SELECT count(*) FROM issue_orchestration_projection_receipt WHERE issue_id = $1`, issueID); count != 1 {
		t.Fatalf("generation conflict created %d receipts, want 1", count)
	}

}
