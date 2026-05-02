const COOKIE_NAME = "multica_logged_in";

export function setLoggedInCookie() {
  document.cookie = `${COOKIE_NAME}=1; path=/; max-age=31536000; samesite=lax`;
}

// Velafi: minimal client-side hint cookie clear.
//
// The full SSO logout (multica HttpOnly cookie clear + Authentik IDP session
// invalidation + redirect to /login) is owned by the server-side route at
// /auth/server-logout — useLogout (packages/views/auth/use-logout.ts)
// navigates the browser there for web platform. This function exists only to
// keep the frontend-readable presence cookie in sync if anything triggers
// onLogout outside that path (e.g. AuthInitializer's failed bootstrap, 401
// from api). Do NOT add fetch / window.location / sessionStorage here:
// those races caused intermittent silent re-login bugs (incident
// 2026-05-02). Keep it boring.
export function clearLoggedInCookie() {
  document.cookie = `${COOKIE_NAME}=; path=/; max-age=0; SameSite=Lax`;
}
