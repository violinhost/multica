// Package managedaction owns the fixed native managed-action tracer surface.
// It intentionally has no dynamic action loading or plugin registry.
package managedaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issueposition"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	ActionKey     = "automultica.start_managed_w1.v1"
	ActionVersion = "v1"
	WorkflowW1    = "managed_w1"
)

var (
	ErrUnknownAction        = errors.New("managed action is unknown")
	ErrActionDisabled       = errors.New("managed action is disabled for project")
	ErrAuthorityUnavailable = errors.New("managed action authority verifier is unavailable")
	ErrAuthorityInvalid     = errors.New("managed action authority receipt is invalid")
	ErrInvalidRequest       = errors.New("invalid managed action request")
)

// Spec is a static, versioned capability. New managed actions are source
// changes, not rows supplied by callers.
type Spec struct {
	Key, Version, Workflow string
}

func Registry() []Spec { return []Spec{{Key: ActionKey, Version: ActionVersion, Workflow: WorkflowW1}} }

func FindSpec(key, version string) (Spec, bool) {
	for _, spec := range Registry() {
		if spec.Key == key && spec.Version == version {
			return spec, true
		}
	}
	return Spec{}, false
}

// AuthorityVerifier is deliberately opaque. VEL-4629 owns receipt issuance;
// this tracer only asks whether a receipt authorizes the exact request facts.
type AuthorityVerifier interface {
	VerifyManagedAction(ctx context.Context, receipt json.RawMessage, workspaceID, projectID, parentIssueID, agentID string, resourceIDs []string, revisionFacts json.RawMessage) error
}

type Request struct {
	SchemaVersion    string          `json:"schema_version"`
	RequestID        string          `json:"request_id"`
	IdempotencyKey   string          `json:"idempotency_key"`
	WorkspaceID      string          `json:"workspace_id"`
	ProjectID        string          `json:"project_id"`
	ParentIssueID    string          `json:"parent_issue_id"`
	ActionKey        string          `json:"action_key"`
	ActionVersion    string          `json:"action_version"`
	Workflow         string          `json:"workflow"`
	InitialRole      string          `json:"initial_role"`
	InitialStage     int32           `json:"initial_stage"`
	ReleasePolicy    string          `json:"release_policy"`
	PrimaryAgentID   string          `json:"primary_agent_id"`
	ResourceIDs      []string        `json:"resource_ids"`
	RevisionFacts    json.RawMessage `json:"revision_facts"`
	AuthorityReceipt json.RawMessage `json:"authority_receipt"`
	Source           string          `json:"source"`
	Actor            string          `json:"actor"`
}

type Receipt struct {
	DispatchID    string `json:"dispatch_id"`
	WorkspaceID   string `json:"workspace_id"`
	ProjectID     string `json:"project_id"`
	ParentIssueID string `json:"parent_issue_id"`
	ActionKey     string `json:"action_key"`
	Generation    int    `json:"generation"`
	State         string `json:"state"`
	ChildIssueID  string `json:"child_issue_id"`
	TaskID        string `json:"task_id"`
}

type Capability struct {
	Key, Version, Workflow string
	Enabled                bool
}

type Service struct {
	Queries     *db.Queries
	DB          db.DBTX
	TxStarter   service.TxStarter
	Verifier    AuthorityVerifier
	TaskService *service.TaskService
	// MaterializeHook is a narrow failure-injection seam used by transaction
	// boundary tests. Production services leave it nil.
	MaterializeHook func(stage string) error
}

func NewService(q *db.Queries, database db.DBTX, tx service.TxStarter, verifier AuthorityVerifier, tasks *service.TaskService) *Service {
	return &Service{Queries: q, DB: database, TxStarter: tx, Verifier: verifier, TaskService: tasks}
}

func (s *Service) Capabilities(ctx context.Context, workspaceID, projectID pgtype.UUID) ([]Capability, error) {
	var enabled bool
	_, err := s.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: projectID, WorkspaceID: workspaceID})
	if err != nil {
		return nil, fmt.Errorf("project: %w", err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT enabled FROM managed_action_enablement WHERE workspace_id=$1 AND project_id=$2 AND action_key=$3`, workspaceID, projectID, ActionKey).Scan(&enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			enabled = false
		} else {
			return nil, err
		}
	}
	return []Capability{{Key: ActionKey, Version: ActionVersion, Workflow: WorkflowW1, Enabled: enabled}}, nil
}

func (s *Service) Start(ctx context.Context, req Request) (Receipt, error) {
	spec, ok := FindSpec(req.ActionKey, req.ActionVersion)
	if !ok {
		return Receipt{}, ErrUnknownAction
	}
	if err := validateRequest(req, spec); err != nil {
		return Receipt{}, err
	}
	if s.Verifier == nil {
		return Receipt{}, ErrAuthorityUnavailable
	}
	ws, err := util.ParseUUID(req.WorkspaceID)
	if err != nil {
		return Receipt{}, ErrInvalidRequest
	}
	projectID, err := util.ParseUUID(req.ProjectID)
	if err != nil {
		return Receipt{}, ErrInvalidRequest
	}
	parentID, err := util.ParseUUID(req.ParentIssueID)
	if err != nil {
		return Receipt{}, ErrInvalidRequest
	}
	agentID, err := util.ParseUUID(req.PrimaryAgentID)
	if err != nil {
		return Receipt{}, ErrInvalidRequest
	}
	actorID, err := util.ParseUUID(req.Actor)
	if err != nil {
		return Receipt{}, ErrInvalidRequest
	}

	resources, err := s.validateScope(ctx, ws, projectID, parentID, agentID, req.ResourceIDs)
	if err != nil {
		return Receipt{}, err
	}
	if err := s.Verifier.VerifyManagedAction(ctx, req.AuthorityReceipt, req.WorkspaceID, req.ProjectID, req.ParentIssueID, req.PrimaryAgentID, req.ResourceIDs, req.RevisionFacts); err != nil {
		return Receipt{}, fmt.Errorf("%w: %v", ErrAuthorityInvalid, err)
	}

	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return Receipt{}, fmt.Errorf("begin dispatch: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := s.Queries.WithTx(tx)
	bindings, _ := json.Marshal(resources)
	snapshot, _ := json.Marshal(req)
	var dispatchID pgtype.UUID
	insertErr := tx.QueryRow(ctx, `INSERT INTO managed_action_dispatch (workspace_id,project_id,parent_issue_id,action_key,action_version,idempotency_key,workflow,primary_agent_id,resource_bindings,revision_facts,authority_receipt,request_snapshot)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (workspace_id,project_id,parent_issue_id,action_key,idempotency_key,generation) DO NOTHING RETURNING id`,
		ws, projectID, parentID, req.ActionKey, req.ActionVersion, req.IdempotencyKey, req.Workflow, agentID, bindings, req.RevisionFacts, req.AuthorityReceipt, snapshot).Scan(&dispatchID)
	if insertErr != nil && !errors.Is(insertErr, pgx.ErrNoRows) {
		return Receipt{}, fmt.Errorf("create dispatch: %w", insertErr)
	}
	if err := s.materializationBoundary("dispatch"); err != nil {
		return Receipt{}, err
	}
	if !dispatchID.Valid {
		r, err := scanReceipt(ctx, tx, ws, projectID, parentID, req.ActionKey, req.IdempotencyKey)
		if err != nil {
			return Receipt{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Receipt{}, err
		}
		return r, nil
	}

	number, err := qtx.IncrementIssueCounter(ctx, ws)
	if err != nil {
		return Receipt{}, err
	}
	position, err := issueposition.NextTopPosition(ctx, tx, ws, "todo")
	if err != nil {
		return Receipt{}, err
	}
	child, err := qtx.CreateIssue(ctx, db.CreateIssueParams{WorkspaceID: ws, Title: "Managed W1 analysis", Description: pgtype.Text{String: "Managed action analysis lane", Valid: true}, Status: "todo", Priority: "high", AssigneeType: pgtype.Text{String: "agent", Valid: true}, AssigneeID: agentID, CreatorType: "member", CreatorID: actorID, ParentIssueID: parentID, Position: position, Number: number, ProjectID: projectID, Stage: pgtype.Int4{Int32: 1, Valid: true}})
	if err != nil {
		return Receipt{}, fmt.Errorf("create analysis child: %w", err)
	}
	if err := s.materializationBoundary("child"); err != nil {
		return Receipt{}, err
	}
	metadata, _ := json.Marshal(map[string]any{"managed_action_dispatch_id": util.UUIDToString(dispatchID), "managed_action_key": req.ActionKey, "managed_action_generation": 1, "managed_action_resources": req.ResourceIDs, "managed_action_revision_facts": json.RawMessage(req.RevisionFacts)})
	if _, err := tx.Exec(ctx, `UPDATE issue SET metadata=$2 WHERE id=$1`, child.ID, metadata); err != nil {
		return Receipt{}, err
	}
	agent, err := qtx.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: ws})
	if err != nil {
		return Receipt{}, ErrInvalidRequest
	}
	if agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
		return Receipt{}, ErrInvalidRequest
	}
	task, err := qtx.CreateAgentTask(ctx, db.CreateAgentTaskParams{AgentID: agentID, RuntimeID: agent.RuntimeID, IssueID: child.ID, Priority: 3, TriggerSummary: pgtype.Text{String: "Managed W1 analysis", Valid: true}, OriginatorUserID: actorID, AccountableUserID: actorID, OriginatorSource: pgtype.Text{String: "managed_action", Valid: true}, TriggerEvidenceKind: pgtype.Text{String: "managed_action_dispatch", Valid: true}, TriggerEvidenceRefID: dispatchID})
	if err != nil {
		return Receipt{}, fmt.Errorf("create analysis task: %w", err)
	}
	if err := s.materializationBoundary("task"); err != nil {
		return Receipt{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE managed_action_dispatch SET child_issue_id=$2, task_id=$3 WHERE id=$1`, dispatchID, child.ID, task.ID); err != nil {
		return Receipt{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO managed_action_outbox (dispatch_id,task_id,runtime_id) VALUES ($1,$2,$3)`, dispatchID, task.ID, agent.RuntimeID); err != nil {
		return Receipt{}, err
	}
	if err := s.materializationBoundary("outbox"); err != nil {
		return Receipt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Receipt{}, fmt.Errorf("commit dispatch: %w", err)
	}
	r := Receipt{DispatchID: util.UUIDToString(dispatchID), WorkspaceID: req.WorkspaceID, ProjectID: req.ProjectID, ParentIssueID: req.ParentIssueID, ActionKey: req.ActionKey, Generation: 1, State: "analysis_queued", ChildIssueID: util.UUIDToString(child.ID), TaskID: util.UUIDToString(task.ID)}
	return r, nil
}

func (s *Service) materializationBoundary(stage string) error {
	if s.MaterializeHook == nil {
		return nil
	}
	return s.MaterializeHook(stage)
}

func (s *Service) Reconcile(ctx context.Context) {
	if s == nil || s.DB == nil || s.TaskService == nil {
		return
	}
	if s.TaskService.Wakeup == nil {
		return
	}
	rows, err := s.DB.Query(ctx, `SELECT o.id, o.task_id, o.runtime_id FROM managed_action_outbox o WHERE o.status='pending' OR (o.status='delivering' AND o.updated_at < now() - interval '1 minute') ORDER BY o.created_at LIMIT 100`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var outboxID, taskID, runtimeID pgtype.UUID
		if rows.Scan(&outboxID, &taskID, &runtimeID) != nil {
			continue
		}
		var claimed bool
		if err := s.DB.QueryRow(ctx, `UPDATE managed_action_outbox SET status='delivering', attempts=attempts+1, updated_at=now() WHERE id=$1 AND (status='pending' OR (status='delivering' AND updated_at < now() - interval '1 minute')) RETURNING true`, outboxID).Scan(&claimed); err != nil || !claimed {
			continue
		}
		s.TaskService.Wakeup.NotifyTaskAvailable(util.UUIDToString(runtimeID), util.UUIDToString(taskID))
		_, _ = s.DB.Exec(ctx, `UPDATE managed_action_outbox SET status='delivered', delivered_at=now(), updated_at=now() WHERE id=$1 AND status='delivering'`, outboxID)
	}
}

// ObserveTaskTerminal records the native task outcome for this dispatch. It is
// intentionally observational: a generic task terminal signal cannot mark a
// managed workflow successful without VEL-4629 typed evidence verification.
func (s *Service) ObserveTaskTerminal(ctx context.Context, task db.AgentTaskQueue) error {
	if s == nil || s.DB == nil || !task.ID.Valid {
		return nil
	}
	var dispatchID pgtype.UUID
	if err := s.DB.QueryRow(ctx, `SELECT id FROM managed_action_dispatch WHERE task_id=$1`, task.ID).Scan(&dispatchID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			// Mixed-version rollout: task lifecycle remains available while a
			// node waits for the managed-action migration to be applied.
			return nil
		}
		return err
	}
	state := "analysis_terminal_" + task.Status
	if task.Status != "completed" && task.Status != "failed" && task.Status != "cancelled" {
		state = "analysis_terminal_unknown"
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO managed_action_lane_observation (dispatch_id, task_id, lane_role, stage, task_status, failure_reason)
		VALUES ($1,$2,'analysis',1,$3,$4) ON CONFLICT (dispatch_id, task_id) DO NOTHING`, dispatchID, task.ID, task.Status, task.FailureReason)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `UPDATE managed_action_dispatch SET state=$2, terminal_observation=jsonb_build_object('task_id',$3::text,'task_status',$4), updated_at=now() WHERE id=$1 AND state NOT IN ('succeeded','failed')`, dispatchID, state, task.ID, task.Status)
	return err
}

func (s *Service) validateScope(ctx context.Context, ws, projectID, parentID, agentID pgtype.UUID, resourceIDs []string) ([]map[string]any, error) {
	if _, err := s.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: projectID, WorkspaceID: ws}); err != nil {
		return nil, ErrInvalidRequest
	}
	parent, err := s.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: parentID, WorkspaceID: ws})
	if err != nil || parent.ProjectID != projectID || parent.Status == "done" || parent.Status == "cancelled" {
		return nil, ErrInvalidRequest
	}
	if _, err := s.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: ws}); err != nil {
		return nil, ErrInvalidRequest
	}
	var enabled bool
	if err := s.DB.QueryRow(ctx, `SELECT enabled FROM managed_action_enablement WHERE workspace_id=$1 AND project_id=$2 AND action_key=$3`, ws, projectID, ActionKey).Scan(&enabled); err != nil || !enabled {
		return nil, ErrActionDisabled
	}
	projectResources, err := s.Queries.ListProjectResources(ctx, projectID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]db.ProjectResource, len(projectResources))
	for _, resource := range projectResources {
		byID[util.UUIDToString(resource.ID)] = resource
	}
	out := make([]map[string]any, 0, len(resourceIDs))
	for _, id := range resourceIDs {
		resource, ok := byID[id]
		if !ok {
			return nil, ErrInvalidRequest
		}
		out = append(out, map[string]any{"id": id, "type": resource.ResourceType, "ref": json.RawMessage(resource.ResourceRef)})
	}
	return out, nil
}

func validateRequest(req Request, spec Spec) error {
	if req.SchemaVersion != "v1" || strings.TrimSpace(req.RequestID) == "" || strings.TrimSpace(req.IdempotencyKey) == "" || req.Workflow != spec.Workflow || req.InitialRole != "analysis" || req.InitialStage != 1 || strings.TrimSpace(req.ReleasePolicy) == "" || strings.TrimSpace(req.Source) == "" || strings.TrimSpace(req.Actor) == "" || len(req.ResourceIDs) == 0 || !json.Valid(req.RevisionFacts) || !json.Valid(req.AuthorityReceipt) {
		return ErrInvalidRequest
	}
	seen := map[string]struct{}{}
	for _, id := range req.ResourceIDs {
		if _, err := util.ParseUUID(id); err != nil {
			return ErrInvalidRequest
		}
		if _, dup := seen[id]; dup {
			return ErrInvalidRequest
		}
		seen[id] = struct{}{}
	}
	var revision struct {
		Revision string `json:"revision"`
	}
	if json.Unmarshal(req.RevisionFacts, &revision) != nil || !isImmutableRevision(revision.Revision) {
		return ErrInvalidRequest
	}
	return nil
}

func isImmutableRevision(revision string) bool {
	if len(revision) != 40 {
		return false
	}
	for _, r := range revision {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func scanReceipt(ctx context.Context, tx pgx.Tx, ws, projectID, parentID pgtype.UUID, actionKey, idempotencyKey string) (Receipt, error) {
	var r Receipt
	var id, child, task pgtype.UUID
	err := tx.QueryRow(ctx, `SELECT id, action_key, generation, state, child_issue_id, task_id FROM managed_action_dispatch WHERE workspace_id=$1 AND project_id=$2 AND parent_issue_id=$3 AND action_key=$4 AND idempotency_key=$5 AND generation=1`, ws, projectID, parentID, actionKey, idempotencyKey).Scan(&id, &r.ActionKey, &r.Generation, &r.State, &child, &task)
	if err != nil {
		return Receipt{}, fmt.Errorf("load dispatch receipt: %w", err)
	}
	r.DispatchID, r.WorkspaceID, r.ProjectID, r.ParentIssueID = util.UUIDToString(id), util.UUIDToString(ws), util.UUIDToString(projectID), util.UUIDToString(parentID)
	r.ChildIssueID, r.TaskID = util.UUIDToString(child), util.UUIDToString(task)
	return r, nil
}
