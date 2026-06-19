-- velafi-lark-inbox-pack: cross-event-type dedup for comment-anchored inbox
-- notifications. Multica emits BOTH a `mentioned` and a `new_comment` inbox
-- item for a single comment that @mentions a recipient, which would otherwise
-- send two identical Lark cards. The upstream per-inbox_item delivery ledger
-- (lark_inbox_notification_delivery, migration 122) cannot collapse them
-- because the two items have distinct ids. This velafi-only table claims one
-- delivery PER COMMENT (+ installation + recipient) so only the first sibling
-- event sends. Disposable ledger; drop on retire — 122 stays verbatim upstream.
CREATE TABLE lark_inbox_comment_delivery (
    comment_id UUID NOT NULL REFERENCES comment(id) ON DELETE CASCADE,
    installation_id UUID NOT NULL REFERENCES lark_installation(id) ON DELETE CASCADE,
    lark_open_id TEXT NOT NULL,
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (comment_id, installation_id, lark_open_id)
);
