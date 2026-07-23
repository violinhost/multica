package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func recordTaskSupersessionReceipt(t *testing.T, actorUserID, taskID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequestAs(actorUserID, http.MethodPost, "/api/tasks/"+taskID+"/supersession-receipt", body)
	req = withURLParam(req, "taskId", taskID)
	testHandler.RecordTaskSupersessionReceipt(w, req)
	return w
}

func createForeignSupersessionWorkspace(t *testing.T) (workspaceID, userID string) {
	t.Helper()
	ctx := context.Background()

	email := "foreign-supersession-" + time.Now().Format("150405.000000") + "@example.test"
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('Foreign Supersession User', $1) RETURNING id
	`, email).Scan(&userID); err != nil {
		t.Fatalf("create foreign user: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Foreign Supersession WS', $1, 'foreign supersession test', 'FSW')
		RETURNING id
	`, handlerTestWorkspaceSlug+"-foreign-"+time.Now().Format("150405")).Scan(&workspaceID); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, workspaceID, userID); err != nil {
		t.Fatalf("add foreign owner: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return workspaceID, userID
}

func createSupersessionReceiptFixture(t *testing.T) (runtimeID, agentID, issueID, taskID, commentA, commentB string) {
	t.Helper()
	ctx := context.Background()
	runtimeID = createClaimReclaimRuntime(t, ctx, "Receipt API runtime")
	agentID, issueID = createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Receipt API agent")
	commentA = insertSupersessionComment(t, ctx, issueID, "receipt older scope")
	commentB = insertSupersessionComment(t, ctx, issueID, "receipt newer scope")
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			trigger_comment_id, coalesced_comment_ids
		)
		VALUES ($1, $2, $3, 'queued', 10, $4, ARRAY[$5::uuid])
		RETURNING id
	`, agentID, runtimeID, issueID, commentB, commentA).Scan(&taskID); err != nil {
		t.Fatalf("insert queued receipt task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
	})
	return runtimeID, agentID, issueID, taskID, commentA, commentB
}

func TestRecordTaskSupersessionReceipt_SucceedsForWorkspaceOwner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID, agentID, issueID, taskID, commentA, commentB := createSupersessionReceiptFixture(t)
	var supersedingTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			started_at, completed_at
		)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '2 minutes', now() - interval '1 minute')
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&supersedingTaskID); err != nil {
		t.Fatalf("insert superseding task: %v", err)
	}

	w := recordTaskSupersessionReceipt(t, testUserID, taskID, map[string]any{
		"superseded_by_task_id": supersedingTaskID,
		"superseded_comment_ids": []string{
			commentB,
			commentA,
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("RecordTaskSupersessionReceipt: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentTaskResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.SupersessionKind != "trusted_receipt" {
		t.Fatalf("supersession_kind = %q, want trusted_receipt", resp.SupersessionKind)
	}
	if resp.SupersededByTaskID == nil || *resp.SupersededByTaskID != supersedingTaskID {
		got := "<nil>"
		if resp.SupersededByTaskID != nil {
			got = *resp.SupersededByTaskID
		}
		t.Fatalf("superseded_by_task_id = %s, want %s", got, supersedingTaskID)
	}
	got := append([]string(nil), resp.SupersededCommentIDs...)
	slices.Sort(got)
	want := []string{commentA, commentB}
	if !slices.Equal(got, want) {
		t.Fatalf("superseded_comment_ids = %v, want %v", got, want)
	}
}

func TestRecordTaskSupersessionReceipt_RejectsPartialCoverage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID, agentID, issueID, taskID, commentA, _ := createSupersessionReceiptFixture(t)
	var supersedingTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			started_at, completed_at
		)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '2 minutes', now() - interval '1 minute')
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&supersedingTaskID); err != nil {
		t.Fatalf("insert superseding task: %v", err)
	}

	w := recordTaskSupersessionReceipt(t, testUserID, taskID, map[string]any{
		"superseded_by_task_id": supersedingTaskID,
		"superseded_comment_ids": []string{
			commentA,
		},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRecordTrustedTaskSupersessionReceiptQuery_RejectsMutatedPlanAtomically(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID, agentID, issueID, taskID, commentA, _ := createSupersessionReceiptFixture(t)
	var supersedingTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			started_at, completed_at
		)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '2 minutes', now() - interval '1 minute')
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&supersedingTaskID); err != nil {
		t.Fatalf("insert superseding task: %v", err)
	}

	_, err := testHandler.Queries.RecordTrustedTaskSupersessionReceipt(ctx, db.RecordTrustedTaskSupersessionReceiptParams{
		TaskID:               parseUUID(taskID),
		SupersededByTaskID:   parseUUID(supersedingTaskID),
		SupersededCommentIds: []pgtype.UUID{parseUUID(commentA)},
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows from atomic query validation, got %v", err)
	}

	audit := loadSupersessionAudit(t, taskID)
	if audit.SupersessionKind != "" || audit.SupersededByTaskID != "" || len(audit.SupersededCommentIDs) != 0 {
		t.Fatalf("mutated-plan query must not write a receipt, got kind=%q task=%q ids=%v", audit.SupersessionKind, audit.SupersededByTaskID, audit.SupersededCommentIDs)
	}
}

func TestRecordTaskSupersessionReceipt_RejectsNonTerminalSupersedingTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID, agentID, issueID, taskID, commentA, commentB := createSupersessionReceiptFixture(t)
	var supersedingTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			dispatched_at
		)
		VALUES ($1, $2, $3, 'dispatched', 0, now() - interval '1 minute')
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&supersedingTaskID); err != nil {
		t.Fatalf("insert non-terminal task: %v", err)
	}

	w := recordTaskSupersessionReceipt(t, testUserID, taskID, map[string]any{
		"superseded_by_task_id": supersedingTaskID,
		"superseded_comment_ids": []string{
			commentA,
			commentB,
		},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRecordTaskSupersessionReceipt_RejectsCrossWorkspaceSupersedingTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	_, _, issueID, taskID, commentA, commentB := createSupersessionReceiptFixture(t)
	foreignWS, foreignUser := createForeignSupersessionWorkspace(t)

	var runtimeID, agentID, supersedingTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at, visibility, owner_id
		)
		VALUES ($1, NULL, 'Foreign Supersession RT', 'cloud', 'handler_test_runtime', 'online', 'x', '{}'::jsonb, now(), 'private', $2)
		RETURNING id
	`, foreignWS, foreignUser).Scan(&runtimeID); err != nil {
		t.Fatalf("insert foreign runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id
		)
		VALUES ($1, 'Foreign Supersession Agent', '', 'cloud', '{}'::jsonb, $2, 'private', 1, $3)
		RETURNING id
	`, foreignWS, runtimeID, foreignUser).Scan(&agentID); err != nil {
		t.Fatalf("insert foreign agent: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'foreign supersession issue', 'in_progress', 'none', $2, 'member', 1, 0)
		RETURNING id
	`, foreignWS, foreignUser).Scan(&issueID); err != nil {
		t.Fatalf("insert foreign issue: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			started_at, completed_at
		)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '2 minutes', now() - interval '1 minute')
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&supersedingTaskID); err != nil {
		t.Fatalf("insert foreign superseding task: %v", err)
	}

	w := recordTaskSupersessionReceipt(t, testUserID, taskID, map[string]any{
		"superseded_by_task_id": supersedingTaskID,
		"superseded_comment_ids": []string{
			commentA,
			commentB,
		},
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRecordTaskSupersessionReceipt_RejectsCrossAgentSupersedingTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	_, _, issueID, taskID, commentA, commentB := createSupersessionReceiptFixture(t)
	otherAgentID := createHandlerTestAgent(t, "Cross-agent supersession", nil)
	var otherRuntimeID string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE id = $1`, otherAgentID).Scan(&otherRuntimeID); err != nil {
		t.Fatalf("load other agent runtime: %v", err)
	}

	var supersedingTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			started_at, completed_at
		)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '2 minutes', now() - interval '1 minute')
		RETURNING id
	`, otherAgentID, otherRuntimeID, issueID).Scan(&supersedingTaskID); err != nil {
		t.Fatalf("insert cross-agent superseding task: %v", err)
	}

	w := recordTaskSupersessionReceipt(t, testUserID, taskID, map[string]any{
		"superseded_by_task_id": supersedingTaskID,
		"superseded_comment_ids": []string{
			commentA,
			commentB,
		},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRecordTaskSupersessionReceipt_RejectsPlainWorkspaceMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	memberUserID := handlerWorkspaceMember(t, "supersessionMember")
	runtimeID, agentID, issueID, taskID, commentA, commentB := createSupersessionReceiptFixture(t)
	var supersedingTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			started_at, completed_at
		)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '2 minutes', now() - interval '1 minute')
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&supersedingTaskID); err != nil {
		t.Fatalf("insert superseding task: %v", err)
	}

	w := recordTaskSupersessionReceipt(t, memberUserID, taskID, map[string]any{
		"superseded_by_task_id": supersedingTaskID,
		"superseded_comment_ids": []string{
			commentA,
			commentB,
		},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}
