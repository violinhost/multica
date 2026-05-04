-- Velafi fork: Lark bot OAuth-style consent flow.
-- A row is created when the bot first DMs a user without a stored PAT.
-- The user opens the deep-link in Lark webview (already SSO-authenticated
-- to Multica via Phase A), clicks "授权", which creates a PAT scoped to
-- "lark-bot-<user>" and writes its plaintext into pat_token_plaintext.
-- The bot then polls /api/lark-bot/poll/:id, retrieves the plaintext,
-- transitions status=consumed and NULLs the plaintext column.
CREATE TABLE lark_bot_session (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- workspace_id is set when the user authorizes (we pick their first
    -- workspace then). The bot can't know it at init time.
    workspace_id UUID REFERENCES workspace(id) ON DELETE CASCADE,
    multica_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    lark_open_id TEXT NOT NULL,
    lark_union_id TEXT NOT NULL,
    pat_token_id UUID REFERENCES personal_access_token(id) ON DELETE SET NULL,
    pat_token_plaintext TEXT,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'authorized', 'consumed', 'expired', 'revoked')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    authorized_at TIMESTAMPTZ,
    consumed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '15 minutes'
);

CREATE INDEX idx_lark_bot_session_lark_union ON lark_bot_session(lark_union_id, status);
CREATE INDEX idx_lark_bot_session_status_expiry ON lark_bot_session(status, expires_at);
