package lark

import (
	"context"
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
		LarkUserBinding: db.LarkUserBinding{LarkOpenID: openID},
		LarkInstallation: db.LarkInstallation{
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
		if !ok || row.LarkUserBinding.LarkOpenID != "ou_a" {
			t.Fatalf("want actor agent A (ou_a), got ok=%v open=%q", ok, row.LarkUserBinding.LarkOpenID)
		}
	})

	t.Run("falls back when no agent bot applies (human→human)", func(t *testing.T) {
		rows := []db.ListActiveLarkUserBindingsByMemberRow{
			bindingRowFor(t, fallback, "ou_f"),
		}
		// Human actor, no issue → only the fallback can deliver.
		item := inboxNotificationItem{Type: "mentioned"}
		row, ok := selectInboxNotificationBinding(ctx, nil, rows, item, uuidFor(t, fallback))
		if !ok || row.LarkUserBinding.LarkOpenID != "ou_f" {
			t.Fatalf("want fallback (ou_f), got ok=%v open=%q", ok, row.LarkUserBinding.LarkOpenID)
		}
	})

	t.Run("falls back when acting agent not bound by recipient", func(t *testing.T) {
		// Actor is agent B but the recipient is only bound to the fallback.
		rows := []db.ListActiveLarkUserBindingsByMemberRow{
			bindingRowFor(t, fallback, "ou_f"),
		}
		item := inboxNotificationItem{ActorType: &agentType, ActorID: &agentB}
		row, ok := selectInboxNotificationBinding(ctx, nil, rows, item, uuidFor(t, fallback))
		if !ok || row.LarkUserBinding.LarkOpenID != "ou_f" {
			t.Fatalf("want fallback (ou_f), got ok=%v open=%q", ok, row.LarkUserBinding.LarkOpenID)
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
