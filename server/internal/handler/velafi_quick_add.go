// velafi_quick_add.go — Velafi-fork-only handler for "add member by email
// without invitation roundtrip". Admin types a name (frontend resolves to
// email via roster), backend creates a stub user (if needed) + member row
// in one transaction.
//
// Stub users have external_user_id = NULL; auth_oidc.go's existing
// fallback-by-email path links them to a real Authentik sub on first
// OIDC login. Until then, frontend renders a "pending first login"
// badge for users with external_user_id IS NULL.
//
// File is fork-only — upstream is untouched. Only fork-touched upstream
// file is router.go (one r.Post line).

package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Velafi tenant email domains allowed for quick-add. Anyone outside these
// domains must go through the regular invitation flow (which gates by
// allow-list). This is a guardrail against accidental external adds.
var velafiAllowedDomains = []string{
	"velafi.com",
	"galactic.holdings",
}

type VelafiQuickAddRequest struct {
	Email string `json:"email"`
	Role  string `json:"role,omitempty"` // optional: "admin" | "member" | "readonly"; default "member"
}

type VelafiQuickAddResponse struct {
	User           UserResponse           `json:"user"`
	Member         MemberWithUserResponse `json:"member"`
	IsPendingLogin bool                   `json:"is_pending_login"` // true iff user.external_user_id was NULL
	WasUserCreated bool                   `json:"was_user_created"` // true iff stub user was created (vs reusing existing)
}

// VelafiQuickAdd — POST /api/workspaces/{workspaceId}/velafi/quick-add
//
// Flow:
//  1. Caller must be authenticated + admin/owner of the workspace.
//  2. Email must be in Velafi tenant domain whitelist.
//  3. Lookup user by email.
//     - found → reuse user.id
//     - not found → create stub user (external_user_id NULL, name from email-local-part)
//  4. Insert member row (UPSERT-by-unique semantics; 409 on duplicate).
//  5. MarkUserOnboarded (COALESCE — idempotent).
//  6. Broadcast member:added event so other clients see it instantly.
func (h *Handler) VelafiQuickAdd(w http.ResponseWriter, r *http.Request) {
	// Auth: caller must be authenticated.
	callerID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	callerUUID, ok := parseUUIDOrBadRequest(w, callerID, "user_id")
	if !ok {
		return
	}

	// Workspace from URL/context.
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	// Authorization: caller must be admin/owner of the workspace.
	caller, ok := ctxMember(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "not a workspace member")
		return
	}
	if !roleAllowed(caller.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only workspace owners and admins can quick-add members")
		return
	}

	// Decode request.
	var req VelafiQuickAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	if !isVelafiAllowedDomain(email) {
		writeError(w, http.StatusBadRequest, "email domain not allowed; quick-add is restricted to Velafi tenant domains")
		return
	}

	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = "member"
	}
	switch role {
	case "owner", "admin", "member", "readonly":
		// ok
	default:
		writeError(w, http.StatusBadRequest, "role must be one of: owner, admin, member, readonly")
		return
	}

	// Transaction: stub-create-if-missing + member-create atomically.
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		slog.Error("velafi-quick-add: tx begin failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	qtx := h.Queries.WithTx(tx)

	// Step 1: lookup user by email.
	var (
		user           db.User
		wasUserCreated bool
	)
	user, err = qtx.GetUserByEmail(r.Context(), email)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Error("velafi-quick-add: user lookup failed", "err", err, "email", email)
			writeError(w, http.StatusInternalServerError, "failed to look up user")
			return
		}
		// Step 2: stub-create user. Name = local part of email until OIDC
		// login fills in real name. external_user_id stays NULL — that's
		// our "pending login" signal, picked up by auth_oidc.go's
		// fallback-by-email path on first login.
		stubName := localPartOf(email)
		user, err = qtx.CreateUser(r.Context(), db.CreateUserParams{
			Name:      stubName,
			Email:     email,
			AvatarUrl: pgtype.Text{},
		})
		if err != nil {
			slog.Error("velafi-quick-add: stub user create failed", "err", err, "email", email)
			writeError(w, http.StatusInternalServerError, "failed to create stub user")
			return
		}
		wasUserCreated = true
	}

	// Step 3: insert member.
	member, err := qtx.CreateMember(r.Context(), db.CreateMemberParams{
		WorkspaceID: wsUUID,
		UserID:      user.ID,
		Role:        role,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "user is already a member of this workspace")
			return
		}
		slog.Error("velafi-quick-add: member create failed", "err", err, "user_id", uuidToString(user.ID), "workspace_id", workspaceID)
		writeError(w, http.StatusInternalServerError, "failed to create membership")
		return
	}

	// Step 4: idempotent onboarded marker.
	if _, err := qtx.MarkUserOnboarded(r.Context(), user.ID); err != nil {
		slog.Error("velafi-quick-add: mark onboarded failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to mark user onboarded")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("velafi-quick-add: tx commit failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	isPending := !user.ExternalUserID.Valid

	slog.Info("velafi-quick-add: member added",
		"caller_id", uuidToString(callerUUID),
		"workspace_id", workspaceID,
		"target_email", email,
		"user_id", uuidToString(user.ID),
		"member_id", uuidToString(member.ID),
		"role", role,
		"was_user_created", wasUserCreated,
		"is_pending_login", isPending,
	)

	// Broadcast member:added so existing clients update their member lists.
	memberResp := h.memberWithUserResponse(member, user)
	eventPayload := map[string]any{"member": memberResp}
	if ws, err := h.Queries.GetWorkspace(r.Context(), wsUUID); err == nil {
		eventPayload["workspace_name"] = ws.Name
	}
	h.publish(protocol.EventMemberAdded, workspaceID, "member", callerID, eventPayload)

	writeJSON(w, http.StatusCreated, VelafiQuickAddResponse{
		User:           h.userToResponse(user),
		Member:         memberResp,
		IsPendingLogin: isPending,
		WasUserCreated: wasUserCreated,
	})
}

// isVelafiAllowedDomain reports whether email's domain is in the Velafi
// tenant whitelist (case-insensitive). email is assumed pre-trimmed and
// lowercased by the caller.
func isVelafiAllowedDomain(email string) bool {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return false
	}
	domain := email[at+1:]
	for _, allowed := range velafiAllowedDomains {
		if domain == allowed {
			return true
		}
	}
	return false
}

// localPartOf returns the part of an email before "@", or "" if absent.
// Used as a default display name for stub users until OIDC fills in the
// real name on first login.
func localPartOf(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return email
	}
	return email[:at]
}
