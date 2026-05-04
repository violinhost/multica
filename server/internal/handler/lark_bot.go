package handler

// Velafi fork: Lark bot OAuth-style consent flow.
//
// The customer-bot (running on agentrunner2@us-dallas-exec-004-s) cannot
// reach the user's browser cookies, so it cannot piggyback on the user's
// OIDC session. Instead, when the bot first DMs a user without a stored
// PAT, it kicks off this flow:
//
//   1. bot   → POST /api/lark-bot/init-session       (admin auth)
//                creates a `pending` lark_bot_session row.
//
//   2. user  → opens Lark webview /lark-bot-authorize?s=<id>
//                already SSO-authenticated to Multica via Phase A.
//                clicks [授权]; web posts to:
//              POST /api/lark-bot/authorize           (user OIDC auth)
//                creates a PAT named "lark-bot-<user>" + writes its
//                plaintext into pat_token_plaintext + status=authorized.
//
//   3. bot   → GET /api/lark-bot/poll/:id             (admin auth)
//                returns the plaintext (once), transitions to consumed,
//                NULLs the plaintext column.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---- request/response shapes ----

type LarkBotInitSessionRequest struct {
	LarkOpenID  string `json:"lark_open_id"`
	LarkUnionID string `json:"lark_union_id"`
}

type LarkBotInitSessionResponse struct {
	SessionID string `json:"session_id"`
	ExpiresAt string `json:"expires_at"`
}

type LarkBotAuthorizeRequest struct {
	SessionID   string `json:"session_id"`
	WorkspaceID string `json:"workspace_id"`
}

type LarkBotAuthorizeResponse struct {
	OK bool `json:"ok"`
}

type LarkBotPollResponse struct {
	Status string  `json:"status"`            // pending / authorized / consumed / expired / revoked
	Token  *string `json:"token,omitempty"`   // present once when transitioning authorized → consumed
	UserID *string `json:"user_id,omitempty"` // multica_user_id once authorized
}

// ---- 1. init-session (bot admin auth) ----

func (h *Handler) LarkBotInitSession(w http.ResponseWriter, r *http.Request) {
	var req LarkBotInitSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.LarkOpenID) == "" || strings.TrimSpace(req.LarkUnionID) == "" {
		writeError(w, http.StatusBadRequest, "lark_open_id and lark_union_id are required")
		return
	}

	session, err := h.Queries.CreateLarkBotSession(r.Context(), db.CreateLarkBotSessionParams{
		LarkOpenID:  req.LarkOpenID,
		LarkUnionID: req.LarkUnionID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	writeJSON(w, http.StatusCreated, LarkBotInitSessionResponse{
		SessionID: uuidToString(session.ID),
		ExpiresAt: timestampToString(session.ExpiresAt),
	})
}

// ---- 2. authorize (user OIDC auth, user must be Velafi member) ----

func (h *Handler) LarkBotAuthorize(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req LarkBotAuthorizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sessionUUID, ok := parseUUIDOrBadRequest(w, req.SessionID, "session_id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, req.WorkspaceID, "workspace_id")
	if !ok {
		return
	}

	// Look up the pending session.
	session, err := h.Queries.GetLarkBotSession(r.Context(), sessionUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}
	if session.Status != "pending" {
		writeError(w, http.StatusConflict, fmt.Sprintf("session is %s, not pending", session.Status))
		return
	}

	// Verify the OIDC user is a member of the workspace they chose.
	if _, err := h.getWorkspaceMember(r.Context(), userID, req.WorkspaceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusForbidden, "not a member of the target workspace")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to verify membership")
		return
	}

	// Mint a fresh PAT. No expires_at — bot uses it indefinitely until
	// revoked from Settings → API Tokens.
	rawToken, err := auth.GeneratePATToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	prefix := rawToken
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	pat, err := h.Queries.CreatePersonalAccessToken(r.Context(), db.CreatePersonalAccessTokenParams{
		UserID:      parseUUID(userID),
		Name:        fmt.Sprintf("lark-bot-%s", uuidToString(session.ID)[:8]),
		TokenHash:   auth.HashToken(rawToken),
		TokenPrefix: prefix,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mint pat")
		return
	}

	// Stamp the session: authorized + workspace + plaintext token attached.
	_, err = h.Queries.AuthorizeLarkBotSession(r.Context(), db.AuthorizeLarkBotSessionParams{
		ID:                session.ID,
		WorkspaceID:       pgtype.UUID{Bytes: wsUUID.Bytes, Valid: true},
		MulticaUserID:     pgtype.UUID{Bytes: parseUUID(userID).Bytes, Valid: true},
		PatTokenID:        pgtype.UUID{Bytes: pat.ID.Bytes, Valid: true},
		PatTokenPlaintext: pgtype.Text{String: rawToken, Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record authorization")
		return
	}

	writeJSON(w, http.StatusOK, LarkBotAuthorizeResponse{OK: true})
}

// ---- 3. poll (bot admin auth) ----

// LarkBotPoll consumes a session by id, returning the plaintext PAT once.
// Used when the bot still has the session_id from init-session in flight.
func (h *Handler) LarkBotPoll(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionId")
	sessionUUID, ok := parseUUIDOrBadRequest(w, sessionID, "session_id")
	if !ok {
		return
	}

	session, err := h.Queries.GetLarkBotSession(r.Context(), sessionUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}

	resp := LarkBotPollResponse{Status: session.Status}

	if session.Status == "authorized" {
		consumed, err := h.Queries.ConsumeLarkBotSession(r.Context(), session.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to consume session")
			return
		}
		plaintext := session.PatTokenPlaintext.String
		resp.Status = consumed.Status
		resp.Token = &plaintext
		userIDStr := uuidToString(consumed.MulticaUserID)
		if userIDStr != "" {
			resp.UserID = &userIDStr
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// LarkBotPollByUnion is the lazy on-demand variant: bot looks up the
// latest authorized session for a lark_union_id and consumes it. Used
// when the bot didn't store the session_id and just wants "did this
// user authorize me in the last 15 minutes?".
func (h *Handler) LarkBotPollByUnion(w http.ResponseWriter, r *http.Request) {
	unionID := strings.TrimSpace(chi.URLParam(r, "unionId"))
	if unionID == "" {
		writeError(w, http.StatusBadRequest, "union_id is required")
		return
	}

	session, err := h.Queries.GetLatestAuthorizedSessionByUnionID(r.Context(), unionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, LarkBotPollResponse{Status: "pending"})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load session")
		return
	}

	consumed, err := h.Queries.ConsumeLarkBotSession(r.Context(), session.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to consume session")
		return
	}

	plaintext := session.PatTokenPlaintext.String
	resp := LarkBotPollResponse{
		Status: consumed.Status,
		Token:  &plaintext,
	}
	userIDStr := uuidToString(consumed.MulticaUserID)
	if userIDStr != "" {
		resp.UserID = &userIDStr
	}
	writeJSON(w, http.StatusOK, resp)
}

