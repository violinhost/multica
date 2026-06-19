-- velafi-lark-inbox-pack: backport of upstream PR #3919 (feat(lark): deliver
-- inbox notifications as direct cards). Upstream numbers this 118; renumbered
-- to 122 here because our fork is already at 121. Schema is VERBATIM upstream
-- so that when #3919 merges we can retire this cleanly (identical table).
CREATE TABLE lark_inbox_notification_delivery (
    inbox_item_id UUID NOT NULL REFERENCES inbox_item(id) ON DELETE CASCADE,
    installation_id UUID NOT NULL REFERENCES lark_installation(id) ON DELETE CASCADE,
    lark_open_id TEXT NOT NULL,
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (inbox_item_id, installation_id, lark_open_id)
);
