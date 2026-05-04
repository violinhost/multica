// Velafi self-host fork: onboarding flow is dead code. New users come in
// via two paths and BOTH set onboarded_at server-side:
//   - velafi-quick-add (admin direct-add): MarkUserOnboarded in handler
//   - first OIDC login (auth_oidc.go): MarkUserOnboarded right after
//     SetUserExternalIdentity
// resolvePostAuthDestination therefore never routes to /onboarding for
// real Velafi users. This server-side redirect is a defense-in-depth
// catch for stale browser history (back button after rare race) so
// users never see the upstream questionnaire flash.
import { redirect } from "next/navigation";

export default function OnboardingDisabled() {
  redirect("/");
}
