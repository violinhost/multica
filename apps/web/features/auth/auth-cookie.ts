import { configStore } from "@multica/core/config";

const COOKIE_NAME = "multica_logged_in";

export function setLoggedInCookie() {
  document.cookie = `${COOKIE_NAME}=1; path=/; max-age=31536000; samesite=lax`;
}

// Velafi: OIDC RP-initiated logout. Without invalidating Authentik's IDP
// session, silent SSO would re-login the user the moment they hit /login
// after clearing the multica cookie (IDP session still valid → token
// refresh path bypasses any prompt). Send the browser to Authentik's
// default-invalidation-flow which kills the IDP session, then bounces
// back through the login flow (which, with single source bound, lands on
// the Lark login page — clear signal that the user is logged out).
export function clearLoggedInCookie() {
  document.cookie = `${COOKIE_NAME}=; path=/; max-age=0`;

  // Backend logout: clear multica's HttpOnly auth cookie. We use
  // `keepalive: true` so the POST is guaranteed to complete even after
  // window.location.href triggers navigation — without it, the in-flight
  // fetch is canceled, the cookie persists, and the user is silently
  // re-logged-in when they bounce back from Authentik invalidation.
  // /auth/logout is CSRF-exempt at the multica spec level, so a plain
  // fetch (no api wrapper) is safe here.
  try {
    fetch("/auth/logout", {
      method: "POST",
      credentials: "include",
      keepalive: true,
    });
  } catch {
    /* best-effort */
  }

  if (typeof window === "undefined") return;
  const issuer = configStore.getState().oidcIssuerURL;
  if (!issuer) return;
  try {
    // Resolve invalidation-flow URL relative to the issuer so we adapt to
    // both root-path Authentik deployments (issuer like
    // https://idp.example.com/application/o/<app>/) and subpath ones
    // (https://multica.velafi.ai/idp/application/o/<app>/). The 3 ../
    // walks up application/o/<app>/ → application/o/ → application/ → root
    // (or /idp/ in the subpath case).
    const url = new URL(
      "../../../if/flow/default-invalidation-flow/",
      issuer,
    );
    url.searchParams.set("next", `${window.location.origin}/`);
    window.location.href = url.toString();
  } catch {
    /* malformed issuer — fall through, user stays on whatever page they were on */
  }
}
