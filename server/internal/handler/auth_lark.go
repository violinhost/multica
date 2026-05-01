package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Lark Suite (international) enterprise OAuth login. Uses the v2 token
// endpoint which accepts client_id / client_secret directly and does not
// require a prior app_access_token exchange.
//
// The Lark Suite OAuth API is byte-identical to Feishu's — same paths, same
// response shapes, only the host differs. The domestic Feishu variant lives
// behind open.feishu.cn and is tracked separately; international Lark Suite
// lives behind open.larksuite.com.
//
// Docs: https://open.larksuite.com/document/uAjLw4CM/ukTMukTMukTM/authentication-management/access-token/get-user-access-token
const (
	larkTokenURL    = "https://open.larksuite.com/open-apis/authen/v2/oauth/token"
	larkUserInfoURL = "https://open.larksuite.com/open-apis/authen/v1/user_info"
)

type LarkLoginRequest struct {
	Code        string `json:"code"`
	RedirectURI string `json:"redirect_uri"`
}

// larkTokenResponse is the v2 oauth/token success body. Lark Suite follows
// the OAuth 2.0 standard here, so fields match RFC 6749.
type larkTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope"`
}

// larkUserInfoResponse wraps Lark Suite's standard response envelope.
// An email may be absent if the user hasn't bound one; enterprise_email is
// populated when the tenant issues mailboxes, and we prefer it when present.
type larkUserInfoResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Name            string `json:"name"`
		EnName          string `json:"en_name"`
		AvatarURL       string `json:"avatar_url"`
		Email           string `json:"email"`
		EnterpriseEmail string `json:"enterprise_email"`
	} `json:"data"`
}

func (h *Handler) LarkLogin(w http.ResponseWriter, r *http.Request) {
	var req LarkLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	appID := os.Getenv("LARK_APP_ID")
	appSecret := os.Getenv("LARK_APP_SECRET")
	if appID == "" || appSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "Lark login is not configured")
		return
	}

	redirectURI := req.RedirectURI
	if redirectURI == "" {
		redirectURI = os.Getenv("LARK_REDIRECT_URI")
	}

	// Exchange authorization code for a user access token.
	tokenBody, err := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     appID,
		"client_secret": appSecret,
		"code":          req.Code,
		"redirect_uri":  redirectURI,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	tokenReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, larkTokenURL, bytes.NewReader(tokenBody))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	tokenReq.Header.Set("Content-Type", "application/json")

	tokenResp, err := http.DefaultClient.Do(tokenReq)
	if err != nil {
		slog.Error("lark oauth token exchange failed", "error", err)
		writeError(w, http.StatusBadGateway, "failed to exchange code with Lark")
		return
	}
	defer tokenResp.Body.Close()

	tokenRespBytes, err := io.ReadAll(tokenResp.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to read Lark token response")
		return
	}

	if tokenResp.StatusCode != http.StatusOK {
		slog.Error("lark oauth token exchange returned error", "status", tokenResp.StatusCode, "body", string(tokenRespBytes))
		writeError(w, http.StatusBadRequest, "failed to exchange code with Lark")
		return
	}

	var lToken larkTokenResponse
	if err := json.Unmarshal(tokenRespBytes, &lToken); err != nil {
		writeError(w, http.StatusBadGateway, "failed to parse Lark token response")
		return
	}
	if lToken.AccessToken == "" {
		slog.Error("lark oauth token missing from response", "body", string(tokenRespBytes))
		writeError(w, http.StatusBadGateway, "invalid Lark token response")
		return
	}

	// Fetch user profile with the user access token.
	userInfoReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, larkUserInfoURL, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	userInfoReq.Header.Set("Authorization", "Bearer "+lToken.AccessToken)

	userInfoResp, err := http.DefaultClient.Do(userInfoReq)
	if err != nil {
		slog.Error("lark userinfo fetch failed", "error", err)
		writeError(w, http.StatusBadGateway, "failed to fetch user info from Lark")
		return
	}
	defer userInfoResp.Body.Close()

	var lUser larkUserInfoResponse
	if err := json.NewDecoder(userInfoResp.Body).Decode(&lUser); err != nil {
		writeError(w, http.StatusBadGateway, "failed to parse Lark user info")
		return
	}
	if lUser.Code != 0 {
		slog.Error("lark userinfo returned error", "code", lUser.Code, "msg", lUser.Msg)
		writeError(w, http.StatusBadGateway, "failed to fetch user info from Lark")
		return
	}

	// Email is required — we reject login when absent. enterprise_email is
	// the company-issued address (preferred when set), email is the personal
	// one the user linked to their Lark account.
	rawEmail := lUser.Data.EnterpriseEmail
	if rawEmail == "" {
		rawEmail = lUser.Data.Email
	}
	if rawEmail == "" {
		writeError(w, http.StatusBadRequest, "Lark account has no email. Please bind an email to your Lark account or contact your tenant admin to enable enterprise email.")
		return
	}
	email := strings.ToLower(strings.TrimSpace(rawEmail))

	user, isNew, err := h.findOrCreateUser(r.Context(), email)
	if err != nil {
		var signupErr SignupError
		if errors.As(err, &signupErr) {
			writeError(w, http.StatusForbidden, signupErr.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}
	if isNew {
		evt := analytics.Signup(uuidToString(user.ID), user.Email, signupSourceFromRequest(r))
		evt.Properties["auth_method"] = "lark"
		h.Analytics.Capture(evt)
	}

	// Backfill name and avatar from Lark profile if the user was just
	// created (default name is email prefix) or has no avatar yet.
	displayName := lUser.Data.Name
	if displayName == "" {
		displayName = lUser.Data.EnName
	}

	needsUpdate := false
	newName := user.Name
	newAvatar := user.AvatarUrl

	if displayName != "" && user.Name == strings.Split(email, "@")[0] {
		newName = displayName
		needsUpdate = true
	}
	if lUser.Data.AvatarURL != "" && !user.AvatarUrl.Valid {
		newAvatar = pgtype.Text{String: lUser.Data.AvatarURL, Valid: true}
		needsUpdate = true
	}

	if needsUpdate {
		updated, err := h.Queries.UpdateUser(r.Context(), db.UpdateUserParams{
			ID:        user.ID,
			Name:      newName,
			AvatarUrl: newAvatar,
		})
		if err == nil {
			user = updated
		}
	}

	tokenString, err := h.issueJWT(user)
	if err != nil {
		slog.Warn("lark login failed", append(logger.RequestAttrs(r), "error", err, "email", email)...)
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	if err := auth.SetAuthCookies(w, tokenString); err != nil {
		slog.Warn("failed to set auth cookies", "error", err)
	}

	if h.CFSigner != nil {
		for _, cookie := range h.CFSigner.SignedCookies(time.Now().Add(72 * time.Hour)) {
			http.SetCookie(w, cookie)
		}
	}

	slog.Info("user logged in via lark", append(logger.RequestAttrs(r), "user_id", uuidToString(user.ID), "email", user.Email)...)
	writeJSON(w, http.StatusOK, LoginResponse{
		Token: tokenString,
		User:  userToResponse(user),
	})
}
