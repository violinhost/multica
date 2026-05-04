-- name: CreateLarkBotSession :one
INSERT INTO lark_bot_session (lark_open_id, lark_union_id)
VALUES ($1, $2)
RETURNING *;

-- name: GetLarkBotSession :one
SELECT * FROM lark_bot_session WHERE id = $1;

-- name: AuthorizeLarkBotSession :one
UPDATE lark_bot_session
SET status = 'authorized',
    workspace_id = $2,
    multica_user_id = $3,
    pat_token_id = $4,
    pat_token_plaintext = $5,
    authorized_at = now()
WHERE id = $1
  AND status = 'pending'
  AND expires_at > now()
RETURNING *;

-- name: ConsumeLarkBotSession :one
UPDATE lark_bot_session
SET status = 'consumed',
    consumed_at = now(),
    pat_token_plaintext = NULL
WHERE id = $1
  AND status = 'authorized'
RETURNING *;

-- name: GetLatestAuthorizedSessionByUnionID :one
SELECT * FROM lark_bot_session
WHERE lark_union_id = $1
  AND status = 'authorized'
  AND expires_at > now()
ORDER BY authorized_at DESC NULLS LAST
LIMIT 1;

-- name: ExpireStaleLarkBotSessions :execrows
UPDATE lark_bot_session
SET status = 'expired',
    pat_token_plaintext = NULL
WHERE status IN ('pending', 'authorized')
  AND expires_at <= now();
