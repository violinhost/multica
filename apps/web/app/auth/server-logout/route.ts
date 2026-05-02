import { type NextRequest, NextResponse } from "next/server";

// Velafi: SSO logout entry point.
//
// useLogout (packages/views/auth/use-logout.ts) navigates the browser here
// for web platform. We do everything server-side so the chain is atomic:
//
//   1. Set-Cookie all multica auth cookies with Max-Age=0 (multica_auth
//      HttpOnly + Strict, multica_csrf Strict, multica_logged_in Lax) —
//      definitive, no race with frontend keepalive POST /auth/logout.
//   2. 302 → Authentik invalidation flow URL with `?next=` pointing back
//      to /login. Authentik clears the IDP session, then 302's to /login.
//   3. /login then auto-redirects through OIDC authorize, hits Authentik
//      with no session → renders fresh Multica Velafi SSO sign-in.
//
// Bypassing the previous frontend-only logout flow (auth store onLogout +
// keepalive POST + sessionStorage flag) eliminates the race where Plan B
// /login's redirect-to-OIDC effect would override our invalidation
// navigation and silently re-log the user back in via still-valid IDP
// session.
//
// IDP path is hardcoded to /idp/ — matches the Phase 3 same-domain
// reverse-proxy setup. If we ever move the IDP back to its own subdomain
// or change the prefix, update IDP_INVALIDATION_PATH below.
const IDP_INVALIDATION_PATH = "/idp/if/flow/default-invalidation-flow/";

export function GET(request: NextRequest) {
  // Build public origin from X-Forwarded-Host + X-Forwarded-Proto since
  // Next.js sees the internal Docker bind (0.0.0.0:3000) for request.url
  // when running behind cloudflared.
  const xfHost = request.headers.get("x-forwarded-host");
  const xfProto = request.headers.get("x-forwarded-proto") || "https";
  const origin =
    xfHost && /^[A-Za-z0-9.\-]+(:\d+)?$/.test(xfHost)
      ? `${xfProto}://${xfHost}`
      : "";

  const invalidation = origin
    ? new URL(IDP_INVALIDATION_PATH, origin)
    : new URL(IDP_INVALIDATION_PATH, "https://multica.velafi.ai");
  invalidation.searchParams.set(
    "next",
    `${origin || "https://multica.velafi.ai"}/login`,
  );

  const response = NextResponse.redirect(invalidation, 302);
  response.cookies.set({
    name: "multica_auth",
    value: "",
    path: "/",
    maxAge: 0,
    httpOnly: true,
    sameSite: "strict",
  });
  response.cookies.set({
    name: "multica_csrf",
    value: "",
    path: "/",
    maxAge: 0,
    sameSite: "strict",
  });
  response.cookies.set({
    name: "multica_logged_in",
    value: "",
    path: "/",
    maxAge: 0,
    sameSite: "lax",
  });
  return response;
}
