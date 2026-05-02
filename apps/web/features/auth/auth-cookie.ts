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

  if (typeof window === "undefined") return;
  const issuer = configStore.getState().oidcIssuerURL;
  if (!issuer) return;
  try {
    const idpOrigin = new URL(issuer).origin;
    const url = new URL(`${idpOrigin}/if/flow/default-invalidation-flow/`);
    url.searchParams.set("next", `${window.location.origin}/`);
    window.location.href = url.toString();
  } catch {
    /* malformed issuer — fall through, user stays on whatever page they were on */
  }
}
