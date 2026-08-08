package managedaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type acceptingVerifier struct{ err error }

func (v acceptingVerifier) VerifyManagedAction(context.Context, json.RawMessage, string, string, string, string, []string, json.RawMessage) error {
	return v.err
}

type managedWakeup struct {
	mu    sync.Mutex
	calls []managedWakeupCall
}

type managedWakeupCall struct {
	runtimeID string
	taskID    string
}

func (w *managedWakeup) NotifyTaskAvailable(runtimeID, taskID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, managedWakeupCall{runtimeID: runtimeID, taskID: taskID})
}

func (w *managedWakeup) taskIDs() map[string]struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	ids := make(map[string]struct{}, len(w.calls))
	for _, call := range w.calls {
		ids[call.taskID] = struct{}{}
	}
	return ids
}

func (w *managedWakeup) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.calls)
}

func managedActionPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil || pool.Ping(ctx) != nil {
		if pool != nil {
			pool.Close()
		}
		t.Skip("database unavailable")
	}
	var present bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.managed_action_dispatch') IS NOT NULL`).Scan(&present); err != nil || !present {
		pool.Close()
		t.Skip("managed-action migrations are not applied")
	}
	t.Cleanup(pool.Close)
	return pool
}

type managedFixture struct {
	workspaceID, projectID, parentID, agentID, actorID, resourceID string
}

func newManagedFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) managedFixture {
	t.Helper()
	suffix := time.Now().UnixNano()
	var f managedFixture
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('Managed action test',$1) RETURNING id`, fmt.Sprintf("managed-action-%d@example.test", suffix)).Scan(&f.actorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name,slug,description,issue_prefix) VALUES ('Managed action test',$1,'','MAT') RETURNING id`, fmt.Sprintf("managed-action-%d", suffix)).Scan(&f.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'owner')`, f.workspaceID, f.actorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO project (workspace_id,title,status) VALUES ($1,'Managed action project','in_progress') RETURNING id`, f.workspaceID).Scan(&f.projectID); err != nil {
		t.Fatal(err)
	}
	var runtimeID string
	if err := pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,device_info,metadata,visibility,owner_id) VALUES ($1,'Managed action runtime','cloud','test','online','', '{}'::jsonb,'private',$2) RETURNING id`, f.workspaceID, f.actorID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,description,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks,owner_id) VALUES ($1,'Managed action agent','','cloud','{}'::jsonb,$2,'private',1,$3) RETURNING id`, f.workspaceID, runtimeID, f.actorID).Scan(&f.agentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO issue (workspace_id,title,status,priority,creator_type,creator_id,number,position,project_id) VALUES ($1,'Managed action parent','todo','high','member',$2,900001,0,$3) RETURNING id`, f.workspaceID, f.actorID, f.projectID).Scan(&f.parentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO project_resource (workspace_id,project_id,resource_type,resource_ref,position,created_by) VALUES ($1,$2,'github_repo','{"url":"git@github.com:example/repo.git","ref":"main"}'::jsonb,0,$3) RETURNING id`, f.workspaceID, f.projectID, f.actorID).Scan(&f.resourceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM managed_action_lane_observation WHERE dispatch_id IN (SELECT id FROM managed_action_dispatch WHERE workspace_id=$1)`, f.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM managed_action_outbox WHERE dispatch_id IN (SELECT id FROM managed_action_dispatch WHERE workspace_id=$1)`, f.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM managed_action_dispatch WHERE workspace_id=$1`, f.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM managed_action_enablement WHERE workspace_id=$1`, f.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE agent_id=$1`, f.agentID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM issue WHERE workspace_id=$1`, f.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM project_resource WHERE project_id=$1`, f.projectID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM agent WHERE id=$1`, f.agentID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE workspace_id=$1`, f.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM project WHERE id=$1`, f.projectID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id=$1`, f.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id=$1`, f.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, f.actorID)
	})
	return f
}

func (f managedFixture) request(key string) Request {
	return Request{SchemaVersion: "v1", RequestID: "request-" + key, IdempotencyKey: key, WorkspaceID: f.workspaceID, ProjectID: f.projectID, ParentIssueID: f.parentID, ActionKey: ActionKey, ActionVersion: ActionVersion, Workflow: WorkflowW1, InitialRole: "analysis", InitialStage: 1, ReleasePolicy: "draft", PrimaryAgentID: f.agentID, ResourceIDs: []string{f.resourceID}, RevisionFacts: json.RawMessage(`{"revision":"0123456789abcdef0123456789abcdef01234567"}`), AuthorityReceipt: json.RawMessage(`{"test":true}`), Source: "test", Actor: f.actorID}
}

func managedCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ws string) [4]int {
	t.Helper()
	var dispatches, children, tasks, outbox int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM managed_action_dispatch WHERE workspace_id=$1`, ws).Scan(&dispatches); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issue WHERE workspace_id=$1 AND parent_issue_id IS NOT NULL`, ws).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue q JOIN managed_action_dispatch d ON d.task_id=q.id WHERE d.workspace_id=$1`, ws).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM managed_action_outbox o JOIN managed_action_dispatch d ON d.id=o.dispatch_id WHERE d.workspace_id=$1`, ws).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	return [4]int{dispatches, children, tasks, outbox}
}

func TestManagedActionDBAcceptance(t *testing.T) {
	ctx := context.Background()
	pool := managedActionPool(t)
	fixture := newManagedFixture(t, ctx, pool)
	queries := db.New(pool)
	svc := NewService(queries, pool, pool, acceptingVerifier{}, nil)

	t.Run("validation failures mutate nothing", func(t *testing.T) {
		before := managedCounts(t, ctx, pool, fixture.workspaceID)
		if _, err := svc.Start(ctx, fixture.request("disabled")); !errors.Is(err, ErrActionDisabled) {
			t.Fatalf("disabled action = %v", err)
		}
		if _, err := svc.SetEnabled(ctx, util.MustParseUUID(fixture.workspaceID), util.MustParseUUID(fixture.projectID), true); err != nil {
			t.Fatal(err)
		}
		cases := []struct {
			req  Request
			want error
		}{
			{req: fixture.request("unknown"), want: ErrUnknownAction},
			{req: fixture.request("malformed"), want: ErrInvalidRequest},
			{req: fixture.request("foreign"), want: ErrInvalidRequest},
		}
		cases[0].req.ActionKey = "unknown"
		cases[1].req.RevisionFacts = json.RawMessage(`{"revision":"not-a-sha"}`)
		cases[2].req.ResourceIDs = []string{"00000000-0000-0000-0000-000000000000"}
		for _, tc := range cases {
			if _, err := svc.Start(ctx, tc.req); !errors.Is(err, tc.want) {
				t.Fatalf("Start(%s) error = %v, want %v", tc.req.IdempotencyKey, err, tc.want)
			}
		}
		unavailable := NewService(queries, pool, pool, nil, nil)
		if _, err := unavailable.Start(ctx, fixture.request("missing-verifier")); !errors.Is(err, ErrAuthorityUnavailable) {
			t.Fatalf("missing verifier = %v", err)
		}
		invalid := NewService(queries, pool, pool, acceptingVerifier{err: errors.New("stale")}, nil)
		if _, err := invalid.Start(ctx, fixture.request("invalid-verifier")); !errors.Is(err, ErrAuthorityInvalid) {
			t.Fatalf("invalid verifier = %v", err)
		}
		if after := managedCounts(t, ctx, pool, fixture.workspaceID); after != before {
			t.Fatalf("validation mutated rows: before=%v after=%v", before, after)
		}
	})

	receipt, err := svc.Start(ctx, fixture.request("accepted"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := managedCounts(t, ctx, pool, fixture.workspaceID); got != [4]int{1, 1, 1, 1} {
		t.Fatalf("materialization counts = %v", got)
	}
	replayed, err := svc.Start(ctx, fixture.request("accepted"))
	if err != nil || replayed != receipt {
		t.Fatalf("replay receipt=%+v err=%v, want %+v", replayed, err, receipt)
	}

	var race Receipt
	t.Run("concurrent same key converges", func(t *testing.T) {
		const workers = 2
		start := make(chan struct{})
		results := make(chan Receipt, workers)
		errs := make(chan error, workers)
		for range workers {
			go func() { <-start; r, err := svc.Start(ctx, fixture.request("race")); results <- r; errs <- err }()
		}
		close(start)
		var first Receipt
		for range workers {
			r := <-results
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
			if first.DispatchID == "" {
				first = r
			} else if r != first {
				t.Fatalf("race receipts diverged: %+v %+v", first, r)
			}
		}
		race = first
		if got := managedCounts(t, ctx, pool, fixture.workspaceID); got != [4]int{2, 2, 2, 2} {
			t.Fatalf("race counts = %v", got)
		}
	})

	// A request accepted after the service is already running wakes its own
	// task immediately. The earlier accepted/race rows remain pending to model
	// a prior wakeup outage and must not be redelivered by this fast path.
	wakeNow := &managedWakeup{}
	live := NewService(queries, pool, pool, acceptingVerifier{}, &service.TaskService{Wakeup: wakeNow})
	normal, err := live.Start(ctx, fixture.request("normal"))
	if err != nil {
		t.Fatalf("normal Start: %v", err)
	}
	if wakeNow.count() != 1 {
		t.Fatalf("normal-start wakeup calls = %d, want 1", wakeNow.count())
	}
	if _, ok := wakeNow.taskIDs()[normal.TaskID]; !ok {
		t.Fatalf("normal-start did not wake task %s", normal.TaskID)
	}
	var normalStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM managed_action_outbox WHERE dispatch_id=$1`, util.MustParseUUID(normal.DispatchID)).Scan(&normalStatus); err != nil || normalStatus != "delivered" {
		t.Fatalf("normal outbox status=%q err=%v", normalStatus, err)
	}
	if got := managedCounts(t, ctx, pool, fixture.workspaceID); got != [4]int{3, 3, 3, 3} {
		t.Fatalf("normal-start counts = %v", got)
	}

	t.Run("every materialization boundary rolls back", func(t *testing.T) {
		for _, boundary := range []string{"dispatch", "child", "task", "outbox"} {
			broken := NewService(queries, pool, pool, acceptingVerifier{}, nil)
			broken.MaterializeHook = func(stage string) error {
				if stage == boundary {
					return errors.New("injected")
				}
				return nil
			}
			if _, err := broken.Start(ctx, fixture.request("rollback-"+boundary)); err == nil {
				t.Fatalf("%s did not fail", boundary)
			}
			if got := managedCounts(t, ctx, pool, fixture.workspaceID); got != [4]int{3, 3, 3, 3} {
				t.Fatalf("%s leaked rows: %v", boundary, got)
			}
		}
	})

	t.Run("restart reconcile and terminal observation are idempotent", func(t *testing.T) {
		wake := &managedWakeup{}
		restarted := NewService(queries, pool, pool, acceptingVerifier{}, &service.TaskService{Wakeup: wake})
		restarted.Reconcile(ctx)
		restarted.Reconcile(ctx)
		if wake.count() != 2 {
			t.Fatalf("restart wakeup calls = %d, want 2 pending dispatches", wake.count())
		}
		for _, taskID := range []string{receipt.TaskID, race.TaskID} {
			if _, ok := wake.taskIDs()[taskID]; !ok {
				t.Fatalf("restart did not wake pending task %s", taskID)
			}
		}
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM managed_action_outbox WHERE dispatch_id=$1`, util.MustParseUUID(receipt.DispatchID)).Scan(&status); err != nil || status != "delivered" {
			t.Fatalf("outbox status=%q err=%v", status, err)
		}
		task, err := queries.GetAgentTask(ctx, util.MustParseUUID(receipt.TaskID))
		if err != nil {
			t.Fatal(err)
		}
		task.Status = "completed"
		if err := restarted.ObserveTaskTerminal(ctx, task); err != nil {
			t.Fatal(err)
		}
		if err := restarted.ObserveTaskTerminal(ctx, task); err != nil {
			t.Fatal(err)
		}
		var state string
		var observations int
		if err := pool.QueryRow(ctx, `SELECT state FROM managed_action_dispatch WHERE id=$1`, util.MustParseUUID(receipt.DispatchID)).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM managed_action_lane_observation WHERE dispatch_id=$1`, util.MustParseUUID(receipt.DispatchID)).Scan(&observations); err != nil {
			t.Fatal(err)
		}
		if state != "analysis_terminal_completed" || observations != 1 {
			t.Fatalf("terminal state=%q observations=%d", state, observations)
		}
	})

	t.Run("parent and project scopes isolate idempotency", func(t *testing.T) {
		var otherParent string
		if err := pool.QueryRow(ctx, `INSERT INTO issue (workspace_id,title,status,priority,creator_type,creator_id,number,position,project_id) VALUES ($1,'Other parent','todo','high','member',$2,900002,1,$3) RETURNING id`, fixture.workspaceID, fixture.actorID, fixture.projectID).Scan(&otherParent); err != nil {
			t.Fatal(err)
		}
		req := fixture.request("accepted")
		req.ParentIssueID = otherParent
		if _, err := svc.Start(ctx, req); err != nil {
			t.Fatal(err)
		}
		if got := managedCounts(t, ctx, pool, fixture.workspaceID); got != [4]int{4, 4, 4, 4} {
			t.Fatalf("parent isolation counts = %v", got)
		}

		var otherProject, otherResource, projectParent string
		if err := pool.QueryRow(ctx, `INSERT INTO project (workspace_id,title,status) VALUES ($1,'Other managed action project','in_progress') RETURNING id`, fixture.workspaceID).Scan(&otherProject); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO project_resource (workspace_id,project_id,resource_type,resource_ref,position,created_by) VALUES ($1,$2,'github_repo','{"url":"git@github.com:example/other.git","ref":"main"}'::jsonb,0,$3) RETURNING id`, fixture.workspaceID, otherProject, fixture.actorID).Scan(&otherResource); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SetEnabled(ctx, util.MustParseUUID(fixture.workspaceID), util.MustParseUUID(otherProject), true); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO issue (workspace_id,title,status,priority,creator_type,creator_id,number,position,project_id) VALUES ($1,'Other project parent','todo','high','member',$2,900003,2,$3) RETURNING id`, fixture.workspaceID, fixture.actorID, otherProject).Scan(&projectParent); err != nil {
			t.Fatal(err)
		}
		projectRequest := fixture.request("accepted")
		projectRequest.ProjectID, projectRequest.ParentIssueID, projectRequest.ResourceIDs = otherProject, projectParent, []string{otherResource}
		if _, err := svc.Start(ctx, projectRequest); err != nil {
			t.Fatal(err)
		}
		if got := managedCounts(t, ctx, pool, fixture.workspaceID); got != [4]int{5, 5, 5, 5} {
			t.Fatalf("project isolation counts = %v", got)
		}
	})
}
