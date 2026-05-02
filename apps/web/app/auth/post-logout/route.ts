import { type NextRequest, NextResponse } from "next/server";

// Velafi: post-Authentik-invalidation landing.
//
// Authentik's invalidation flow `?next=` parameter points here so cookie
// clearing is GUARANTEED by server-side code, not race-dependent on the
// frontend's keepalive POST /auth/logout (which can be canceled by the
// preceding window.location.href navigation in some browsers).
//
// We Set-Cookie all multica auth cookies with Max-Age=0 to ensure the
// browser drops them, then 302 to /login so the user lands at the SSO
// entry point with a clean session.
//
// Cookie names match server/internal/auth/cookie.go:
//   - multica_auth    (HttpOnly session JWT)
//   - multica_csrf    (CSRF double-submit token)
//   - multica_logged_in (frontend-readable presence flag)
export function GET(request: NextRequest) {
  // Build the public-facing /login URL from X-Forwarded-* headers, since
  // Next.js sees the internal Docker bind (0.0.0.0:3000) for request.url
  // when running behind cloudflared. Fall back to nextUrl, then a relative
  // path, in that order.
  const xfHost = request.headers.get("x-forwarded-host");
  const xfProto = request.headers.get("x-forwarded-proto") || "https";
  const target =
    xfHost && /^[A-Za-z0-9.\-]+(:\d+)?$/.test(xfHost)
      ? `${xfProto}://${xfHost}/login`
      : "/login";
  const response = NextResponse.redirect(target, 302);
  // Match path + sameSite of the originals (server/internal/auth/cookie.go)
  // so the browser identifies and drops them. multica_auth is HttpOnly +
  // SameSite=Strict; multica_csrf is non-HttpOnly + Strict; multica_logged_in
  // is non-HttpOnly + Lax.
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
