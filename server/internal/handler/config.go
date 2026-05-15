package handler

import (
	"net/http"
	"os"
	"strings"
)

type AppConfig struct {
	CdnDomain string `json:"cdn_domain"`
	// Public auth config consumed by the web app at runtime so self-hosted
	// deployments do not need to rebuild the frontend image when operators
	// toggle signup or wire Google/OIDC auth.
	AllowSignup               bool   `json:"allow_signup"`
	GoogleClientID            string `json:"google_client_id,omitempty"`
	OIDCIssuerURL             string `json:"oidc_issuer_url,omitempty"`
	OIDCClientID              string `json:"oidc_client_id,omitempty"`
	OIDCAuthorizationEndpoint string `json:"oidc_authorization_endpoint,omitempty"`
	OIDCEndSessionEndpoint    string `json:"oidc_end_session_endpoint,omitempty"`
	OIDCRedirectURI           string `json:"oidc_redirect_uri,omitempty"`
	AuthCallbackPath          string `json:"auth_callback_path,omitempty"`

	// PostHog public config for the frontend. The key is the same Project
	// API Key the backend uses; returning it here (instead of baking it
	// into the frontend bundle via NEXT_PUBLIC_*) means self-hosted
	// instances — whose server returns an empty key — automatically
	// disable frontend event shipping too.
	PosthogKey  string `json:"posthog_key"`
	PosthogHost string `json:"posthog_host"`
}

// GetConfig is mounted on the public (unauthenticated) route group because
// the web app calls it before login to decide whether to render the Google
// sign-in button and signup UI. Only add fields here that are safe to expose
// to anonymous callers — never user- or tenant-scoped data.
func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	config := AppConfig{
		AllowSignup:     os.Getenv("ALLOW_SIGNUP") != "false",
		GoogleClientID:  os.Getenv("GOOGLE_CLIENT_ID"),
		OIDCIssuerURL:   os.Getenv("OIDC_ISSUER_URL"),
		OIDCClientID:    os.Getenv("OIDC_CLIENT_ID"),
		OIDCRedirectURI: os.Getenv("OIDC_REDIRECT_URI"),
	}
	if config.OIDCIssuerURL != "" {
		trimmed := strings.TrimRight(config.OIDCIssuerURL, "/")
		if strings.HasSuffix(trimmed, "/application/o/multica") {
			config.OIDCAuthorizationEndpoint = strings.TrimSuffix(trimmed, "/multica") + "/authorize/"
			config.OIDCEndSessionEndpoint = trimmed + "/end-session/"
		} else {
			config.OIDCAuthorizationEndpoint = trimmed + "/authorize/"
			config.OIDCEndSessionEndpoint = trimmed + "/end-session/"
		}
		config.AuthCallbackPath = "/auth/oidc/callback"
	}
	if h.Storage != nil {
		config.CdnDomain = h.Storage.CdnDomain()
	}

	// Re-read from env on every request so operators can rotate keys via
	// secret refresh without a server restart.
	if v := os.Getenv("ANALYTICS_DISABLED"); v != "true" && v != "1" {
		config.PosthogKey = os.Getenv("POSTHOG_API_KEY")
		config.PosthogHost = os.Getenv("POSTHOG_HOST")
		if config.PosthogHost == "" && config.PosthogKey != "" {
			config.PosthogHost = "https://us.i.posthog.com"
		}
	}

	writeJSON(w, http.StatusOK, config)
}
