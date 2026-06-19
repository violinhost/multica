package lark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const inboxNotifyTimeout = 10 * time.Second

// inboxNotifierFallbackAgentSettingKey is the workspace.settings (jsonb) key
// holding the agent UUID whose bot delivers inbox notifications that have no
// relevant actor/assignee agent bot. velafi-lark-inbox-pack; set via the
// Lark settings tab (owner/admin).
const inboxNotifierFallbackAgentSettingKey = "lark_inbox_notifier_fallback_agent_id"

type InboxNotifierQueries interface {
	DeleteLarkInboxNotificationDelivery(ctx context.Context, arg db.DeleteLarkInboxNotificationDeliveryParams) error
	GetIssue(ctx context.Context, id pgtype.UUID) (db.Issue, error)
	// velafi-lark-inbox-pack: resolve the comment referenced by a
	// new_comment / mentioned inbox event so the card can show its text.
	GetComment(ctx context.Context, id pgtype.UUID) (db.Comment, error)
	GetNotificationPreference(ctx context.Context, arg db.GetNotificationPreferenceParams) (db.NotificationPreference, error)
	GetWorkspace(ctx context.Context, id pgtype.UUID) (db.Workspace, error)
	ClaimLarkInboxNotificationDelivery(ctx context.Context, arg db.ClaimLarkInboxNotificationDeliveryParams) (bool, error)
	// velafi-lark-inbox-pack: per-comment delivery claim, collapses the
	// mentioned + new_comment pair for one comment into a single card.
	ClaimLarkInboxCommentDelivery(ctx context.Context, arg db.ClaimLarkInboxCommentDeliveryParams) (bool, error)
	DeleteLarkInboxCommentDelivery(ctx context.Context, arg db.DeleteLarkInboxCommentDeliveryParams) error
	ListActiveLarkUserBindingsByMember(ctx context.Context, arg db.ListActiveLarkUserBindingsByMemberParams) ([]db.ListActiveLarkUserBindingsByMemberRow, error)
}

type InboxNotifier struct {
	queries     InboxNotifierQueries
	credentials CredentialsResolver
	client      APIClient
	log         *slog.Logger
	publicURL   string
}

type InboxNotifierConfig struct {
	Logger    *slog.Logger
	PublicURL string
}

func NewInboxNotifier(queries InboxNotifierQueries, credentials CredentialsResolver, client APIClient, cfg InboxNotifierConfig) *InboxNotifier {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &InboxNotifier{
		queries:     queries,
		credentials: credentials,
		client:      client,
		log:         log,
		publicURL:   strings.TrimRight(strings.TrimSpace(cfg.PublicURL), "/"),
	}
}

func (n *InboxNotifier) Register(bus *events.Bus) {
	if bus == nil {
		return
	}
	bus.Subscribe(protocol.EventInboxNew, func(e events.Event) {
		go n.handleEvent(e)
	})
}

func (n *InboxNotifier) handleEvent(e events.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), inboxNotifyTimeout)
	defer cancel()
	if err := n.notify(ctx, e.Payload); err != nil {
		n.log.Warn("lark inbox notifier: notify failed", "err", err.Error())
	}
}

func (n *InboxNotifier) notify(ctx context.Context, payload any) error {
	if n.queries == nil || n.credentials == nil || n.client == nil {
		return errors.New("notifier not configured")
	}
	item, ok := inboxNotificationItemFromPayload(payload)
	if !ok {
		return errors.New("missing inbox item payload")
	}
	if item.RecipientType != "member" {
		return nil
	}
	itemID, err := scanUUID(item.ID)
	if err != nil {
		return fmt.Errorf("parse inbox item id: %w", err)
	}
	workspaceID, err := scanUUID(item.WorkspaceID)
	if err != nil {
		return fmt.Errorf("parse workspace_id: %w", err)
	}
	recipientID, err := scanUUID(item.RecipientID)
	if err != nil {
		return fmt.Errorf("parse recipient_id: %w", err)
	}
	muted, err := n.isMuted(ctx, workspaceID, recipientID, item.Type)
	if err != nil {
		n.log.Warn("lark inbox notifier: preference lookup failed", "err", err.Error())
	} else if muted {
		return nil
	}
	rows, err := n.queries.ListActiveLarkUserBindingsByMember(ctx, db.ListActiveLarkUserBindingsByMemberParams{
		WorkspaceID:   workspaceID,
		MulticaUserID: recipientID,
	})
	if err != nil {
		return fmt.Errorf("lookup lark binding: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	// velafi-lark-inbox-pack: resolve the workspace's fallback notifier agent
	// (settings key), used only when no actor/assignee agent bot applies —
	// e.g. human→human mentions, or an acting agent the recipient hasn't bound.
	fallbackAgentID := n.fallbackNotifierAgent(ctx, workspaceID)
	row, ok := selectInboxNotificationBinding(ctx, n.queries, rows, item, fallbackAgentID)
	if !ok {
		return nil
	}
	// velafi-lark-inbox-pack: dedup the delivery. For comment-anchored events
	// (a single @mention comment makes multica emit BOTH a `mentioned` and a
	// `new_comment` inbox item for the same recipient) claim PER COMMENT so
	// only one card goes out; for everything else keep the per-inbox-item
	// ledger. Either claim is race-safe via its table PK.
	claimed, release, err := n.claimDelivery(ctx, item, itemID, row.LarkInstallation.ID, row.LarkUserBinding.LarkOpenID)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	creds, err := n.installationCredentials(row.LarkInstallation)
	if err != nil {
		release()
		return err
	}
	cardJSON, err := n.renderInboxNotificationCard(ctx, workspaceID, item)
	if err != nil {
		release()
		return fmt.Errorf("render inbox card: %w", err)
	}
	if _, err := n.client.SendDirectInteractiveCard(ctx, SendDirectCardParams{
		InstallationID: creds,
		OpenID:         OpenID(row.LarkUserBinding.LarkOpenID),
		CardJSON:       cardJSON,
	}); err != nil {
		release()
		return fmt.Errorf("send inbox dm: %w", err)
	}
	return nil
}

func (n *InboxNotifier) releaseDeliveryClaim(arg db.DeleteLarkInboxNotificationDeliveryParams) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := n.queries.DeleteLarkInboxNotificationDelivery(ctx, arg); err != nil {
		n.log.Warn("lark inbox notifier: release delivery claim failed", "err", err.Error())
	}
}

func (n *InboxNotifier) installationCredentials(inst db.LarkInstallation) (InstallationCredentials, error) {
	secret, err := n.credentials.DecryptAppSecret(inst)
	if err != nil {
		return InstallationCredentials{}, fmt.Errorf("decrypt app_secret: %w", err)
	}
	creds := InstallationCredentials{
		AppID:     inst.AppID,
		AppSecret: secret,
		Region:    RegionOrDefault(inst.Region),
	}
	if inst.TenantKey.Valid {
		creds.TenantKey = inst.TenantKey.String
	}
	return creds, nil
}

// fallbackNotifierAgent reads workspace.settings for the configured fallback
// notifier agent. velafi-lark-inbox-pack. Returns an invalid UUID (no
// fallback) on any miss — best-effort, never errors the notify path.
func (n *InboxNotifier) fallbackNotifierAgent(ctx context.Context, workspaceID pgtype.UUID) pgtype.UUID {
	ws, err := n.queries.GetWorkspace(ctx, workspaceID)
	if err != nil || len(ws.Settings) == 0 {
		return pgtype.UUID{}
	}
	var settings map[string]any
	if err := json.Unmarshal(ws.Settings, &settings); err != nil {
		return pgtype.UUID{}
	}
	raw, _ := settings[inboxNotifierFallbackAgentSettingKey].(string)
	if strings.TrimSpace(raw) == "" {
		return pgtype.UUID{}
	}
	id, err := scanUUID(raw)
	if err != nil {
		return pgtype.UUID{}
	}
	return id
}

var inboxNotificationTypeToGroup = map[string]string{
	"issue_assigned":      "assignments",
	"unassigned":          "assignments",
	"assignee_changed":    "assignments",
	"status_changed":      "status_changes",
	"new_comment":         "comments",
	"mentioned":           "comments",
	"priority_changed":    "updates",
	"start_date_changed":  "updates",
	"due_date_changed":    "updates",
	"task_completed":      "agent_activity",
	"task_failed":         "agent_activity",
	"agent_blocked":       "agent_activity",
	"agent_completed":     "agent_activity",
	"quick_create_done":   "agent_activity",
	"quick_create_failed": "agent_activity",
}

func (n *InboxNotifier) isMuted(ctx context.Context, workspaceID, userID pgtype.UUID, notificationType string) (bool, error) {
	group, ok := inboxNotificationTypeToGroup[notificationType]
	if !ok {
		return false, nil
	}
	pref, err := n.queries.GetNotificationPreference(ctx, db.GetNotificationPreferenceParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	var prefs map[string]string
	if err := json.Unmarshal(pref.Preferences, &prefs); err != nil {
		return false, nil
	}
	return prefs[group] == "muted", nil
}

type inboxNotificationItem struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	RecipientType string          `json:"recipient_type"`
	RecipientID   string          `json:"recipient_id"`
	Type          string          `json:"type"`
	Severity      string          `json:"severity"`
	IssueID       *string         `json:"issue_id"`
	Title         string          `json:"title"`
	Body          *string         `json:"body"`
	ActorType     *string         `json:"actor_type"`
	ActorID       *string         `json:"actor_id"`
	Details       json.RawMessage `json:"details"`
}

func inboxNotificationItemFromPayload(payload any) (inboxNotificationItem, bool) {
	m, ok := payload.(map[string]any)
	if !ok {
		return inboxNotificationItem{}, false
	}
	raw, ok := m["item"]
	if !ok {
		return inboxNotificationItem{}, false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return inboxNotificationItem{}, false
	}
	var item inboxNotificationItem
	if err := json.Unmarshal(b, &item); err != nil {
		return inboxNotificationItem{}, false
	}
	return item, item.WorkspaceID != "" && item.RecipientID != ""
}

// selectInboxNotificationBinding picks which bound bot delivers the DM.
// Native first (so the relevant agent notifies as itself), velafi fallback last.
func selectInboxNotificationBinding(ctx context.Context, queries InboxNotifierQueries, rows []db.ListActiveLarkUserBindingsByMemberRow, item inboxNotificationItem, fallbackAgentID pgtype.UUID) (db.ListActiveLarkUserBindingsByMemberRow, bool) {
	// 1. actor agent's own bot (the agent that performed the action).
	if item.ActorType != nil && *item.ActorType == "agent" && item.ActorID != nil {
		if actorID, err := scanUUID(*item.ActorID); err == nil {
			if row, ok := selectInboxNotificationBindingByAgent(rows, actorID); ok {
				return row, true
			}
		}
	}
	// 2. the issue's assignee agent's own bot.
	if item.IssueID != nil && queries != nil {
		if issueID, err := scanUUID(*item.IssueID); err == nil {
			if issue, err := queries.GetIssue(ctx, issueID); err == nil &&
				issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid {
				if row, ok := selectInboxNotificationBindingByAgent(rows, issue.AssigneeID); ok {
					return row, true
				}
			}
		}
	}
	// 3. velafi-lark-inbox-pack: workspace fallback notifier agent. Covers
	// events with no relevant agent bot (human→human mentions, human-driven
	// assignments/status changes) and acting agents the recipient hasn't bound.
	if fallbackAgentID.Valid {
		if row, ok := selectInboxNotificationBindingByAgent(rows, fallbackAgentID); ok {
			return row, true
		}
	}
	return db.ListActiveLarkUserBindingsByMemberRow{}, false
}

func selectInboxNotificationBindingByAgent(rows []db.ListActiveLarkUserBindingsByMemberRow, agentID pgtype.UUID) (db.ListActiveLarkUserBindingsByMemberRow, bool) {
	for _, row := range rows {
		if row.LarkInstallation.AgentID == agentID {
			return row, true
		}
	}
	return db.ListActiveLarkUserBindingsByMemberRow{}, false
}

func (n *InboxNotifier) renderInboxNotificationCard(ctx context.Context, workspaceID pgtype.UUID, item inboxNotificationItem) (string, error) {
	issue, workspace := n.inboxNotificationContext(ctx, workspaceID, item)
	identifier := inboxIssueIdentifier(issue, workspace)
	headerTitle := inboxNotificationHeaderTitle(identifier, item.Title)
	// velafi-lark-inbox-pack: comment / @mention events keep their text in the
	// comment table (the inbox row's body is empty), so resolve it here.
	commentText := n.inboxCommentText(ctx, item)
	bodyMD := inboxNotificationMarkdown(item, commentText)
	if bodyMD == "" {
		// velafi-lark-inbox-pack: events that carry no body of their own
		// (assignment, autopilot creation) would otherwise render as a bare
		// title. Fall back to the issue's own description so the card shows
		// the actual content, not just the headline.
		bodyMD = inboxIssueDescription(issue)
	}
	if bodyMD == "" {
		bodyMD = headerTitle
	}
	card := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": inboxNotificationTemplate(item),
			"title": map[string]any{
				"tag":     "plain_text",
				"content": headerTitle,
			},
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": bodyMD,
				},
			},
		},
	}
	if issueURL := n.issueURL(workspace, item); issueURL != "" {
		card["elements"] = append(card["elements"].([]any),
			map[string]any{"tag": "hr"},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":  "button",
						"text": map[string]any{"tag": "plain_text", "content": "View in Multica"},
						// velafi-lark-inbox-pack: wrap in a larksuite applink so the
						// deep link opens INSIDE the Lark client webview (desktop +
						// mobile) instead of bouncing to an external browser. Same
						// technique as the bind-card button (velafi-lark-ux-pack).
						"url":  larkWebviewURL(issueURL),
						"type": "primary",
					},
				},
			},
		)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (n *InboxNotifier) inboxNotificationContext(ctx context.Context, workspaceID pgtype.UUID, item inboxNotificationItem) (*db.Issue, *db.Workspace) {
	var issue *db.Issue
	if item.IssueID != nil {
		if issueID, err := scanUUID(*item.IssueID); err == nil {
			if row, err := n.queries.GetIssue(ctx, issueID); err == nil {
				issue = &row
			}
		}
	}
	var workspace *db.Workspace
	if row, err := n.queries.GetWorkspace(ctx, workspaceID); err == nil {
		workspace = &row
	}
	return issue, workspace
}

func inboxIssueIdentifier(issue *db.Issue, workspace *db.Workspace) string {
	if issue == nil || issue.Number == 0 {
		return ""
	}
	if workspace != nil && strings.TrimSpace(workspace.IssuePrefix) != "" {
		return fmt.Sprintf("%s-%d", strings.TrimSpace(workspace.IssuePrefix), issue.Number)
	}
	return fmt.Sprintf("#%d", issue.Number)
}

func inboxNotificationHeaderTitle(identifier, title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Inbox"
	}
	if identifier != "" {
		title = fmt.Sprintf("[%s] %s", identifier, title)
	}
	return truncateRunes(title, 80)
}

func inboxNotificationMarkdown(item inboxNotificationItem, commentText string) string {
	// velafi-lark-inbox-pack: prefer the resolved comment text (comment /
	// @mention events store their content in the comment table, not the inbox
	// row body); fall back to the inbox row body for everything else.
	body := strings.TrimSpace(commentText)
	if body == "" && item.Body != nil {
		body = strings.TrimSpace(*item.Body)
	}
	switch item.Type {
	case "new_comment":
		if body == "" {
			return ""
		}
		return fmt.Sprintf("**%s commented**\n\n%s", inboxActorLabel(item), truncateRunes(body, 700))
	case "mentioned":
		// velafi-lark-inbox-pack: show the comment text the mention lives in.
		if body == "" {
			return ""
		}
		return fmt.Sprintf("**%s mentioned you**\n\n%s", inboxActorLabel(item), truncateRunes(body, 700))
	case "status_changed":
		from, to := inboxStatusChange(item)
		if from != "" || to != "" {
			return fmt.Sprintf("**Status changed**\n\n`%s` -> `%s`", statusLabelForInbox(from), statusLabelForInbox(to))
		}
		return "**Status changed**"
	case "quick_create_failed", "task_failed":
		if body != "" {
			return fmt.Sprintf("**Agent task failed**\n\n%s", truncateRunes(body, 700))
		}
		return "**Agent task failed**"
	case "quick_create_done":
		return "**Issue created**"
	default:
		if body != "" {
			return truncateRunes(body, 700)
		}
		return ""
	}
}

// inboxMentionMarkup matches Multica's `[@Name](mention://...)` comment markup
// so it can be flattened to a readable `@Name` in the Lark card.
var inboxMentionMarkup = regexp.MustCompile(`\[@([^\]]+)\]\(mention://[^)]+\)`)

func cleanInboxMentions(s string) string {
	return inboxMentionMarkup.ReplaceAllString(s, "@$1")
}

// inboxCommentText resolves the comment a new_comment / mentioned event points
// at (its id lives in the inbox row's details JSON) and returns its text with
// mention markup flattened. Returns "" on any miss — best-effort, the caller
// falls back to the issue description. velafi-lark-inbox-pack.
// commentIDFromDetails extracts the comment this inbox event points at, if any
// (its id lives in the inbox row's details JSON). velafi-lark-inbox-pack.
func commentIDFromDetails(item inboxNotificationItem) (pgtype.UUID, bool) {
	if len(item.Details) == 0 {
		return pgtype.UUID{}, false
	}
	var d struct {
		CommentID string `json:"comment_id"`
	}
	if err := json.Unmarshal(item.Details, &d); err != nil || d.CommentID == "" {
		return pgtype.UUID{}, false
	}
	id, err := scanUUID(d.CommentID)
	if err != nil {
		return pgtype.UUID{}, false
	}
	return id, true
}

func (n *InboxNotifier) inboxCommentText(ctx context.Context, item inboxNotificationItem) string {
	id, ok := commentIDFromDetails(item)
	if !ok {
		return ""
	}
	c, err := n.queries.GetComment(ctx, id)
	if err != nil {
		return ""
	}
	return cleanInboxMentions(c.Content)
}

// claimDelivery reserves one delivery so concurrent or duplicate events don't
// double-send. Comment-anchored events (the mentioned + new_comment pair for a
// single comment) claim per comment so they collapse to one card; everything
// else claims per inbox item. Both claims are race-safe via their table PK.
// Returns claimed=false when another event already won the claim. The returned
// func releases the claim (call only on a post-claim failure).
// velafi-lark-inbox-pack.
func (n *InboxNotifier) claimDelivery(ctx context.Context, item inboxNotificationItem, itemID, installationID pgtype.UUID, openID string) (bool, func(), error) {
	if commentID, ok := commentIDFromDetails(item); ok {
		claimed, err := n.queries.ClaimLarkInboxCommentDelivery(ctx, db.ClaimLarkInboxCommentDeliveryParams{
			CommentID:      commentID,
			InstallationID: installationID,
			LarkOpenID:     openID,
		})
		if err != nil {
			return false, func() {}, fmt.Errorf("claim lark inbox comment delivery: %w", err)
		}
		arg := db.DeleteLarkInboxCommentDeliveryParams{
			CommentID:      commentID,
			InstallationID: installationID,
			LarkOpenID:     openID,
		}
		return claimed, func() { n.releaseCommentDeliveryClaim(arg) }, nil
	}
	claimed, err := n.queries.ClaimLarkInboxNotificationDelivery(ctx, db.ClaimLarkInboxNotificationDeliveryParams{
		InboxItemID:    itemID,
		InstallationID: installationID,
		LarkOpenID:     openID,
	})
	if err != nil {
		return false, func() {}, fmt.Errorf("claim lark inbox notification delivery: %w", err)
	}
	arg := db.DeleteLarkInboxNotificationDeliveryParams{
		InboxItemID:    itemID,
		InstallationID: installationID,
		LarkOpenID:     openID,
	}
	return claimed, func() { n.releaseDeliveryClaim(arg) }, nil
}

func (n *InboxNotifier) releaseCommentDeliveryClaim(arg db.DeleteLarkInboxCommentDeliveryParams) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := n.queries.DeleteLarkInboxCommentDelivery(ctx, arg); err != nil {
		n.log.Warn("lark inbox notifier: release comment delivery claim failed", "err", err.Error())
	}
}

// inboxIssueDescription returns the issue's own description, trimmed and
// truncated, for use as the card body when the inbox event carries no body
// of its own. velafi-lark-inbox-pack.
func inboxIssueDescription(issue *db.Issue) string {
	if issue == nil || !issue.Description.Valid {
		return ""
	}
	return truncateRunes(strings.TrimSpace(issue.Description.String), 700)
}

func inboxNotificationTemplate(item inboxNotificationItem) string {
	switch item.Type {
	case "new_comment":
		return "wathet"
	case "quick_create_failed", "task_failed":
		return "red"
	case "quick_create_done":
		return "green"
	case "status_changed":
		_, to := inboxStatusChange(item)
		switch to {
		case "done":
			return "green"
		case "blocked", "cancelled":
			return "red"
		case "in_review":
			return "yellow"
		case "in_progress":
			return "blue"
		}
	}
	return "blue"
}

func inboxActorLabel(item inboxNotificationItem) string {
	if item.ActorType != nil && *item.ActorType == "agent" {
		return "Agent"
	}
	return "Someone"
}

func inboxStatusChange(item inboxNotificationItem) (string, string) {
	var details struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if len(item.Details) == 0 {
		return "", ""
	}
	if err := json.Unmarshal(item.Details, &details); err != nil {
		return "", ""
	}
	return details.From, details.To
}

func statusLabelForInbox(s string) string {
	switch s {
	case "backlog":
		return "Backlog"
	case "todo":
		return "Todo"
	case "in_progress":
		return "In progress"
	case "in_review":
		return "In review"
	case "blocked":
		return "Blocked"
	case "done":
		return "Done"
	case "cancelled":
		return "Cancelled"
	case "":
		return "Unknown"
	default:
		return s
	}
}

func (n *InboxNotifier) issueURL(workspace *db.Workspace, item inboxNotificationItem) string {
	if n.publicURL == "" || workspace == nil || workspace.Slug == "" || item.IssueID == nil || *item.IssueID == "" {
		return ""
	}
	return n.publicURL + "/" + url.PathEscape(workspace.Slug) + "/issues/" + url.PathEscape(*item.IssueID)
}

func scanUUID(s string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
