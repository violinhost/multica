package lark

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// velafi-lark-inbox-pack: covers the hybrid bot-selection — native actor/
// assignee agent first, workspace fallback agent last (the velafi addition).

func uuidFor(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("bad uuid %q: %v", s, err)
	}
	return u
}

func bindingRowFor(t *testing.T, agentID, openID string) db.ListActiveLarkUserBindingsByMemberRow {
	return db.ListActiveLarkUserBindingsByMemberRow{
		ChannelUserBinding: db.ChannelUserBinding{ChannelUserID: openID},
		ChannelInstallation: db.ChannelInstallation{
			ID:      uuidFor(t, "11111111-1111-1111-1111-111111111111"),
			AgentID: uuidFor(t, agentID),
		},
	}
}

func TestSelectInboxNotificationBinding_VelafiHybrid(t *testing.T) {
	agentA := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	agentB := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	fallback := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	agentType := "agent"
	ctx := context.Background()

	t.Run("actor agent's own bot wins (native)", func(t *testing.T) {
		rows := []db.ListActiveLarkUserBindingsByMemberRow{
			bindingRowFor(t, agentA, "ou_a"),
			bindingRowFor(t, fallback, "ou_f"),
		}
		item := inboxNotificationItem{ActorType: &agentType, ActorID: &agentA}
		row, ok := selectInboxNotificationBinding(ctx, nil, rows, item, uuidFor(t, fallback))
		if !ok || row.ChannelUserBinding.ChannelUserID != "ou_a" {
			t.Fatalf("want actor agent A (ou_a), got ok=%v open=%q", ok, row.ChannelUserBinding.ChannelUserID)
		}
	})

	t.Run("falls back when no agent bot applies (human→human)", func(t *testing.T) {
		rows := []db.ListActiveLarkUserBindingsByMemberRow{
			bindingRowFor(t, fallback, "ou_f"),
		}
		// Human actor, no issue → only the fallback can deliver.
		item := inboxNotificationItem{Type: "mentioned"}
		row, ok := selectInboxNotificationBinding(ctx, nil, rows, item, uuidFor(t, fallback))
		if !ok || row.ChannelUserBinding.ChannelUserID != "ou_f" {
			t.Fatalf("want fallback (ou_f), got ok=%v open=%q", ok, row.ChannelUserBinding.ChannelUserID)
		}
	})

	t.Run("falls back when acting agent not bound by recipient", func(t *testing.T) {
		// Actor is agent B but the recipient is only bound to the fallback.
		rows := []db.ListActiveLarkUserBindingsByMemberRow{
			bindingRowFor(t, fallback, "ou_f"),
		}
		item := inboxNotificationItem{ActorType: &agentType, ActorID: &agentB}
		row, ok := selectInboxNotificationBinding(ctx, nil, rows, item, uuidFor(t, fallback))
		if !ok || row.ChannelUserBinding.ChannelUserID != "ou_f" {
			t.Fatalf("want fallback (ou_f), got ok=%v open=%q", ok, row.ChannelUserBinding.ChannelUserID)
		}
	})

	t.Run("no match and no fallback configured → no send", func(t *testing.T) {
		rows := []db.ListActiveLarkUserBindingsByMemberRow{
			bindingRowFor(t, agentB, "ou_b"),
		}
		item := inboxNotificationItem{Type: "mentioned"}
		if _, ok := selectInboxNotificationBinding(ctx, nil, rows, item, pgtype.UUID{}); ok {
			t.Fatalf("want no send when nothing matches and no fallback")
		}
	})
}

// velafi-lark-inbox-pack: card body shows comment/mention text (resolved from
// the comment table) and the issue description as fallback, not just a title.
func TestInboxNotificationMarkdown_BodySources(t *testing.T) {
	agentType := "agent"

	t.Run("mention markup is flattened to @Name", func(t *testing.T) {
		raw := "[@Violin](mention://member/abc) Weekly report: 12 done, 3 in review."
		got := cleanInboxMentions(raw)
		if want := "@Violin Weekly report: 12 done, 3 in review."; got != want {
			t.Fatalf("want %q, got %q", want, got)
		}
		if strings.Contains(got, "mention://") {
			t.Fatalf("mention markup should be flattened, got %q", got)
		}
	})

	t.Run("mentioned shows comment text", func(t *testing.T) {
		item := inboxNotificationItem{Type: "mentioned", ActorType: &agentType}
		got := inboxNotificationMarkdown(item, "@Violin Weekly report ready")
		if !strings.Contains(got, "@Violin Weekly report ready") || !strings.Contains(got, "mentioned you") {
			t.Fatalf("want comment text under mention header, got %q", got)
		}
	})

	t.Run("new_comment shows comment text", func(t *testing.T) {
		item := inboxNotificationItem{Type: "new_comment", ActorType: &agentType}
		if got := inboxNotificationMarkdown(item, "hello world"); !strings.Contains(got, "hello world") {
			t.Fatalf("want comment text, got %q", got)
		}
	})

	t.Run("no comment text → empty, caller falls back to description", func(t *testing.T) {
		item := inboxNotificationItem{Type: "mentioned"}
		if got := inboxNotificationMarkdown(item, ""); got != "" {
			t.Fatalf("want empty so caller uses description fallback, got %q", got)
		}
	})
}

func TestInboxIssueDescription_Fallback(t *testing.T) {
	d := "Run the weekly workspace report skill for the last 7 days."
	issue := &db.Issue{Description: pgtype.Text{String: d, Valid: true}}
	if got := inboxIssueDescription(issue); got != d {
		t.Fatalf("want description, got %q", got)
	}
	if got := inboxIssueDescription(nil); got != "" {
		t.Fatalf("nil issue want empty, got %q", got)
	}
}
