// Velafi self-host fork: edge middleware to short-circuit upstream paths
// the fork doesn't expose. Runs before any React server-component code,
// so users never see a meta-refresh flash of the original page.
//
// Currently handles:
//   /onboarding → 307 /login   (questionnaire is dead code in self-host;
//                                see (auth)/onboarding/page.tsx for the
//                                page-level fallback that handles edge
//                                cases the matcher doesn't catch.)
//
// Add new path → /login redirects here as we discover them. The matcher
// glob below MUST be kept narrow — middleware runs on every matched
// request, so accidentally matching `/api/*` or `/_next/*` would tank
// API latency.
import { NextResponse, type NextRequest } from "next/server";

export function middleware(request: NextRequest) {
  if (request.nextUrl.pathname === "/onboarding") {
    return NextResponse.redirect(new URL("/login", request.url), 307);
  }
  return NextResponse.next();
}

export const config = {
  matcher: ["/onboarding"],
};
