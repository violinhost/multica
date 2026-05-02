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

  // Race breaker: the auth store flow runs `set({ user: null })` immediately
  // after this callback returns, which triggers a React re-render. Plan B
  // /login sees `user == null` and would issue its own redirect to the OIDC
  // authorize URL (which silently re-logs the user in if the IDP session is
  // still valid — the very thing we're trying to invalidate). Stamp a flag
  // so Plan B's auto-redirect effect bows out and lets our invalidation
  // navigation win. Plan B clears the flag once the post-invalidation
  // bounce back to /login completes, OR after a 30 s safety window.
  try {
    sessionStorage.setItem(
      "velafi-logout-in-progress",
      String(Date.now()),
    );
  } catch {
    /* sessionStorage may be unavailable (e.g. private mode) — fall through */
  }

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
    // Authentik's `?next=` after invalidation lands at /auth/post-logout,
    // a server-side route that Set-Cookies all multica auth cookies to
    // empty before redirecting to /login. This guarantees the HttpOnly
    // session cookie is cleared even if the frontend's keepalive POST
    // /auth/logout was canceled by the navigation. Without this, a stale
    // backend cookie on the post-invalidation bounce would land the user
    // at the workspace dashboard instead of the SSO sign-in page.
    url.searchParams.set(
      "next",
      `${window.location.origin}/auth/post-logout`,
    );
    window.location.href = url.toString();
  } catch {
    /* malformed issuer — fall through, user stays on whatever page they were on */
  }
}
