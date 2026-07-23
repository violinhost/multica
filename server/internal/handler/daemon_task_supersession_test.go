package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func insertSupersessionComment(t *testing.T, ctx context.Context, issueID, content string) string {
	t.Helper()
	var commentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		VALUES ($1, $2, 'member', $3, $4, 'comment')
		RETURNING id
	`, issueID, testWorkspaceID, testUserID, content).Scan(&commentID); err != nil {
		t.Fatalf("insert comment: %v", err)
	}
	return commentID
}

func claimTaskByRuntimeOnce(t *testing.T, runtimeID, daemonID string) *AgentTaskResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil, testWorkspaceID, daemonID)
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Task *AgentTaskResponse `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	return response.Task
}

type supersessionAuditRow struct {
	Status               string
	Dispatched           bool
	SupersessionKind     string
	SupersededByTaskID   string
	SupersededCommentIDs []string
}

func loadSupersessionAudit(t *testing.T, taskID string) supersessionAuditRow {
	t.Helper()
	var row supersessionAuditRow
	if err := testPool.QueryRow(context.Background(), `
		SELECT
			status,
			dispatched_at IS NOT NULL,
			COALESCE(supersession_kind, ''),
			COALESCE(superseded_by_task_id::text, ''),
			ARRAY(
				SELECT id::text
				FROM unnest(superseded_comment_ids) AS id
				WHERE id IS NOT NULL
				ORDER BY id
			)
		FROM agent_task_queue
		WHERE id = $1
	`, taskID).Scan(
		&row.Status,
		&row.Dispatched,
		&row.SupersessionKind,
		&row.SupersededByTaskID,
		&row.SupersededCommentIDs,
	); err != nil {
		t.Fatalf("load supersession audit: %v", err)
	}
	slices.Sort(row.SupersededCommentIDs)
	return row
}

func TestClaimTaskByRuntime_CancelsTrustedSupersessionReceiptBeforeDispatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createClaimReclaimRuntime(t, ctx, "Trusted supersession runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Trusted supersession agent")
	commentA := insertSupersessionComment(t, ctx, issueID, "older scope")
	commentB := insertSupersessionComment(t, ctx, issueID, "newer scope")
	covered := []string{commentA, commentB}
	slices.Sort(covered)

	var supersedingTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			delivered_comment_ids, started_at, completed_at
		)
		VALUES ($1, $2, $3, 'completed', 0, ARRAY['00000000-0000-0000-0000-000000000111'::uuid], now() - interval '2 minutes', now() - interval '1 minute')
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&supersedingTaskID); err != nil {
		t.Fatalf("insert superseding completed task: %v", err)
	}

	var obsoleteTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			trigger_comment_id, coalesced_comment_ids,
			supersession_kind, superseded_by_task_id, superseded_comment_ids
		)
		VALUES ($1, $2, $3, 'queued', 10, $4, ARRAY[$5::uuid], 'trusted_receipt', $6, ARRAY[$5::uuid, $4::uuid])
		RETURNING id
	`, agentID, runtimeID, issueID, commentB, commentA, supersedingTaskID).Scan(&obsoleteTaskID); err != nil {
		t.Fatalf("insert obsolete queued task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
	})

	if task := claimTaskByRuntimeOnce(t, runtimeID, "trusted-supersession-daemon"); task != nil {
		t.Fatalf("expected no claimed task, got %+v", task)
	}

	audit := loadSupersessionAudit(t, obsoleteTaskID)
	if audit.Status != "cancelled" {
		t.Fatalf("obsolete task status = %s, want cancelled", audit.Status)
	}
	if audit.Dispatched {
		t.Fatal("obsolete trusted-superseded task was dispatched; runtime allocation must stay at zero")
	}
	if audit.SupersessionKind != "trusted_receipt" {
		t.Fatalf("supersession kind = %q, want trusted_receipt", audit.SupersessionKind)
	}
	if audit.SupersededByTaskID != supersedingTaskID {
		t.Fatalf("superseded_by_task_id = %s, want %s", audit.SupersededByTaskID, supersedingTaskID)
	}
	if !slices.Equal(audit.SupersededCommentIDs, covered) {
		t.Fatalf("superseded_comment_ids = %v, want %v", audit.SupersededCommentIDs, covered)
	}
}

func TestClaimTaskByRuntime_CancelsDirectDeliverySupersessionBeforeDispatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createClaimReclaimRuntime(t, ctx, "Direct supersession runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Direct supersession agent")
	commentA := insertSupersessionComment(t, ctx, issueID, "direct older scope")
	commentB := insertSupersessionComment(t, ctx, issueID, "direct newer scope")
	covered := []string{commentA, commentB}
	slices.Sort(covered)

	var obsoleteTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			trigger_comment_id, coalesced_comment_ids, created_at
		)
		VALUES ($1, $2, $3, 'queued', 10, $4, ARRAY[$5::uuid], now() - interval '10 minutes')
		RETURNING id
	`, agentID, runtimeID, issueID, commentB, commentA).Scan(&obsoleteTaskID); err != nil {
		t.Fatalf("insert direct obsolete queued task: %v", err)
	}

	var supersedingTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			delivered_comment_ids, started_at, completed_at,
			created_at
		)
		VALUES ($1, $2, $3, 'completed', 0, ARRAY[$4::uuid, $5::uuid], now() - interval '2 minutes', now() - interval '1 minute', now() - interval '90 seconds')
		RETURNING id
	`, agentID, runtimeID, issueID, commentA, commentB).Scan(&supersedingTaskID); err != nil {
		t.Fatalf("insert later direct completed task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
	})

	if task := claimTaskByRuntimeOnce(t, runtimeID, "direct-supersession-daemon"); task != nil {
		t.Fatalf("expected no claimed task, got %+v", task)
	}

	audit := loadSupersessionAudit(t, obsoleteTaskID)
	if audit.Status != "cancelled" {
		t.Fatalf("obsolete task status = %s, want cancelled", audit.Status)
	}
	if audit.Dispatched {
		t.Fatal("obsolete direct-delivery task was dispatched; runtime allocation must stay at zero")
	}
	if audit.SupersessionKind != "direct_delivery" {
		t.Fatalf("supersession kind = %q, want direct_delivery", audit.SupersessionKind)
	}
	if audit.SupersededByTaskID != supersedingTaskID {
		t.Fatalf("superseded_by_task_id = %s, want %s", audit.SupersededByTaskID, supersedingTaskID)
	}
	if !slices.Equal(audit.SupersededCommentIDs, covered) {
		t.Fatalf("superseded_comment_ids = %v, want %v", audit.SupersededCommentIDs, covered)
	}
}

func TestClaimTaskByRuntime_HistoricalDirectDeliveryDoesNotCancelLaterQueuedTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createClaimReclaimRuntime(t, ctx, "Historical direct runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Historical direct agent")
	commentA := insertSupersessionComment(t, ctx, issueID, "historical older scope")
	commentB := insertSupersessionComment(t, ctx, issueID, "historical newer scope")

	var historicalTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			delivered_comment_ids, started_at, completed_at,
			created_at
		)
		VALUES ($1, $2, $3, 'completed', 0, ARRAY[$4::uuid, $5::uuid], now() - interval '20 minutes', now() - interval '10 minutes', now() - interval '21 minutes')
		RETURNING id
	`, agentID, runtimeID, issueID, commentA, commentB).Scan(&historicalTaskID); err != nil {
		t.Fatalf("insert historical completed task: %v", err)
	}

	var queuedTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			trigger_comment_id, coalesced_comment_ids
		)
		VALUES ($1, $2, $3, 'queued', 10, $4, ARRAY[$5::uuid])
		RETURNING id
	`, agentID, runtimeID, issueID, commentB, commentA).Scan(&queuedTaskID); err != nil {
		t.Fatalf("insert later queued task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
	})

	task := claimTaskByRuntimeOnce(t, runtimeID, "historical-direct-daemon")
	if task == nil {
		t.Fatal("expected later queued task to dispatch; older completed task must not suppress intentional new work")
	}
	if task.ID != queuedTaskID {
		t.Fatalf("claimed task id = %s, want %s", task.ID, queuedTaskID)
	}

	audit := loadSupersessionAudit(t, queuedTaskID)
	if audit.Status != "dispatched" || !audit.Dispatched {
		t.Fatalf("later queued task status = %s dispatched=%v, want dispatched=true", audit.Status, audit.Dispatched)
	}
	if audit.SupersessionKind != "" {
		t.Fatalf("historical direct-delivery must not stamp supersession audit, got %q", audit.SupersessionKind)
	}
}

func TestClaimTaskByRuntime_DirectDeliveryDoesNotCancelRetryOrRerunShapedTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	for _, tc := range []struct {
		name      string
		column    string
		fixtureID string
	}{
		{name: "retry", column: "retry_of_task_id", fixtureID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
		{name: "rerun", column: "rerun_of_task_id", fixtureID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtimeID := createClaimReclaimRuntime(t, ctx, "Direct special-shape runtime "+tc.name)
			agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Direct special-shape agent "+tc.name)
			commentA := insertSupersessionComment(t, ctx, issueID, "special older "+tc.name)
			commentB := insertSupersessionComment(t, ctx, issueID, "special newer "+tc.name)

			if _, err := testPool.Exec(ctx, `
				INSERT INTO agent_task_queue (
					agent_id, runtime_id, issue_id, status, priority,
					delivered_comment_ids, started_at, completed_at,
					created_at
				)
				VALUES ($1, $2, $3, 'completed', 0, ARRAY[$4::uuid, $5::uuid], now() - interval '2 minutes', now() - interval '1 minute', now() - interval '90 seconds')
			`, agentID, runtimeID, issueID, commentA, commentB); err != nil {
				t.Fatalf("insert later covering completed task: %v", err)
			}

			var queuedTaskID string
			query := `
				INSERT INTO agent_task_queue (
					agent_id, runtime_id, issue_id, status, priority,
					trigger_comment_id, coalesced_comment_ids, created_at, ` + tc.column + `
				)
				VALUES ($1, $2, $3, 'queued', 10, $4, ARRAY[$5::uuid], now() - interval '10 minutes', $6::uuid)
				RETURNING id
			`
			if err := testPool.QueryRow(ctx, query, agentID, runtimeID, issueID, commentB, commentA, tc.fixtureID).Scan(&queuedTaskID); err != nil {
				t.Fatalf("insert queued %s task: %v", tc.name, err)
			}
			t.Cleanup(func() {
				testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
			})

			task := claimTaskByRuntimeOnce(t, runtimeID, "direct-shape-"+tc.name)
			if task == nil {
				t.Fatalf("expected %s-shaped queued task to dispatch; direct delivery must fail closed for this shape", tc.name)
			}
			if task.ID != queuedTaskID {
				t.Fatalf("claimed task id = %s, want %s", task.ID, queuedTaskID)
			}
		})
	}
}

func TestClaimTaskByRuntime_PartialTrustedSupersessionReceiptStillClaimsTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	runtimeID := createClaimReclaimRuntime(t, ctx, "Partial supersession runtime")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "Partial supersession agent")
	commentA := insertSupersessionComment(t, ctx, issueID, "partial older scope")
	commentB := insertSupersessionComment(t, ctx, issueID, "partial newer scope")

	var supersedingTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			delivered_comment_ids, started_at, completed_at
		)
		VALUES ($1, $2, $3, 'completed', 0, ARRAY['00000000-0000-0000-0000-000000000222'::uuid], now() - interval '2 minutes', now() - interval '1 minute')
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&supersedingTaskID); err != nil {
		t.Fatalf("insert partial completed task: %v", err)
	}

	var queuedTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			trigger_comment_id, coalesced_comment_ids,
			supersession_kind, superseded_by_task_id, superseded_comment_ids
		)
		VALUES ($1, $2, $3, 'queued', 10, $4, ARRAY[$5::uuid], 'trusted_receipt', $6, ARRAY[$5::uuid])
		RETURNING id
	`, agentID, runtimeID, issueID, commentB, commentA, supersedingTaskID).Scan(&queuedTaskID); err != nil {
		t.Fatalf("insert partial queued task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
	})

	task := claimTaskByRuntimeOnce(t, runtimeID, "partial-supersession-daemon")
	if task == nil {
		t.Fatal("expected task to claim because trusted receipt only covered part of the queued plan")
	}
	if task.ID != queuedTaskID {
		t.Fatalf("claimed task id = %s, want %s", task.ID, queuedTaskID)
	}

	audit := loadSupersessionAudit(t, queuedTaskID)
	if audit.Status != "dispatched" {
		t.Fatalf("task status = %s, want dispatched", audit.Status)
	}
	if !audit.Dispatched {
		t.Fatal("partial/new-scope task failed to dispatch")
	}
	if audit.SupersessionKind != "" || audit.SupersededByTaskID != "" || len(audit.SupersededCommentIDs) != 0 {
		t.Fatalf("dispatch should clear stale supersession hint, got kind=%q task=%q ids=%v", audit.SupersessionKind, audit.SupersededByTaskID, audit.SupersededCommentIDs)
	}
}

func TestClaimTasksByRuntime_BatchSkipsSupersededTaskAndClaimsFreshWork(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	obsoleteRuntimeID := createClaimReclaimRuntime(t, ctx, "Batch superseded runtime")
	obsoleteAgentID, obsoleteIssueID := createClaimReclaimAgentAndIssue(t, ctx, obsoleteRuntimeID, "Batch superseded agent")
	commentA := insertSupersessionComment(t, ctx, obsoleteIssueID, "batch older scope")
	commentB := insertSupersessionComment(t, ctx, obsoleteIssueID, "batch newer scope")
	covered := []string{commentA, commentB}
	slices.Sort(covered)

	var supersedingTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			delivered_comment_ids, started_at, completed_at
		)
		VALUES ($1, $2, $3, 'completed', 0, ARRAY['00000000-0000-0000-0000-000000000333'::uuid], now() - interval '2 minutes', now() - interval '1 minute')
		RETURNING id
	`, obsoleteAgentID, obsoleteRuntimeID, obsoleteIssueID).Scan(&supersedingTaskID); err != nil {
		t.Fatalf("insert batch superseding task: %v", err)
	}

	var obsoleteTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			trigger_comment_id, coalesced_comment_ids,
			supersession_kind, superseded_by_task_id, superseded_comment_ids
		)
		VALUES ($1, $2, $3, 'queued', 10, $4, ARRAY[$5::uuid], 'trusted_receipt', $6, ARRAY[$5::uuid, $4::uuid])
		RETURNING id
	`, obsoleteAgentID, obsoleteRuntimeID, obsoleteIssueID, commentB, commentA, supersedingTaskID).Scan(&obsoleteTaskID); err != nil {
		t.Fatalf("insert batch obsolete queued task: %v", err)
	}

	freshRuntimeID := createClaimReclaimRuntime(t, ctx, "Batch fresh runtime")
	freshAgentID, freshIssueID := createClaimReclaimAgentAndIssue(t, ctx, freshRuntimeID, "Batch fresh agent")
	freshTaskID := seedQueuedIssueTask(t, ctx, freshAgentID, freshRuntimeID, freshIssueID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id IN ($1, $2)`, obsoleteIssueID, freshIssueID)
	})

	w := postBatchClaim(t, testWorkspaceID, []string{obsoleteRuntimeID, freshRuntimeID}, 5)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTasksByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp batchClaimResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode batch response: %v", err)
	}
	if len(resp.Tasks) != 1 {
		t.Fatalf("claimed %d tasks, want exactly 1 fresh task: %s", len(resp.Tasks), w.Body.String())
	}
	if resp.Tasks[0].ID != freshTaskID {
		t.Fatalf("claimed task id = %s, want fresh task %s", resp.Tasks[0].ID, freshTaskID)
	}
	if resp.Tasks[0].RuntimeID != freshRuntimeID {
		t.Fatalf("claimed runtime_id = %s, want %s", resp.Tasks[0].RuntimeID, freshRuntimeID)
	}
	if !strings.HasPrefix(resp.Tasks[0].AuthToken, "mat_") {
		t.Fatalf("fresh task missing mat_ token, got %q", resp.Tasks[0].AuthToken)
	}

	obsoleteAudit := loadSupersessionAudit(t, obsoleteTaskID)
	if obsoleteAudit.Status != "cancelled" {
		t.Fatalf("obsolete batch task status = %s, want cancelled", obsoleteAudit.Status)
	}
	if obsoleteAudit.Dispatched {
		t.Fatal("obsolete batch task was dispatched; batch claim must allocate zero runtime to superseded work")
	}
	if obsoleteAudit.SupersessionKind != "trusted_receipt" {
		t.Fatalf("obsolete batch task kind = %q, want trusted_receipt", obsoleteAudit.SupersessionKind)
	}
	if obsoleteAudit.SupersededByTaskID != supersedingTaskID {
		t.Fatalf("obsolete batch task superseded_by_task_id = %s, want %s", obsoleteAudit.SupersededByTaskID, supersedingTaskID)
	}
	if !slices.Equal(obsoleteAudit.SupersededCommentIDs, covered) {
		t.Fatalf("obsolete batch task superseded_comment_ids = %v, want %v", obsoleteAudit.SupersededCommentIDs, covered)
	}
}
