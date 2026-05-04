// Velafi self-host fork: onboarding flow is dead code. New users come in
// via two paths and BOTH set onboarded_at server-side:
//   - velafi-quick-add (admin direct-add): MarkUserOnboarded in handler
//   - first OIDC login (auth_oidc.go): MarkUserOnboarded right after
//     SetUserExternalIdentity
// resolvePostAuthDestination therefore never routes a real Velafi user
// to /onboarding. This server-side redirect is defense-in-depth for
// stale browser history (back button after rare race) so users never
// see the upstream questionnaire flash.
//
// Redirect target is /login directly (not /) to avoid the prerender
// meta-refresh chain Next.js generates when both endpoints are
// redirects — that produces a cached 200 HTML the user briefly sees.
import { redirect } from "next/navigation";

export const dynamic = "force-dynamic";

export default function OnboardingDisabled() {
  redirect("/login");
}
