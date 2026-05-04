package middleware

// Velafi fork: shared-secret auth gate for the Lark bot's admin endpoints
// (init-session, poll). The bot lives off-host (agentrunner2) and cannot
// hold a per-user OIDC session, so we use a static shared secret set via
// the LARK_BOT_SHARED_SECRET env on both server and bot.

import (
	"crypto/subtle"
	"net/http"
	"os"
)

const larkBotTokenHeader = "X-Lark-Bot-Token"

// LarkBotAdmin verifies the X-Lark-Bot-Token header against
// LARK_BOT_SHARED_SECRET. If the env is unset, the middleware refuses
// everything — there is no implicit-allow fallback.
func LarkBotAdmin() func(http.Handler) http.Handler {
	expected := os.Getenv("LARK_BOT_SHARED_SECRET")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if expected == "" {
				http.Error(w, `{"error":"lark bot admin disabled"}`, http.StatusServiceUnavailable)
				return
			}
			got := r.Header.Get(larkBotTokenHeader)
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
