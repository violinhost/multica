# Multica auth/logout/homepage/onboarding hardening runbook

Date: 2026-05-15
Host: us-dallas-svc-001
Author: Hermes
Purpose: turn repeated upgrade pain around login/logout/homepage/onboarding into an explicit, repeatable contract.

## Why this exists
Recent upgrades repeatedly regressed in the same cluster of surfaces:
- `/login` auto-OIDC behavior
- logout rebound / silent re-login
- split logout implementations (`useLogout` vs store-level `logout`)
- unexpected `/onboarding` landing after OIDC login
- landing/homepage still present even when product intent was login-first
- OIDC user matching failures surfacing as `user registration is disabled...`

This runbook is the anti-repeat layer. Treat it as a required gate for every future upgrade.

---

## A. Product contract to freeze

### A1. Homepage / landing contract
Target contract (Velafi self-host):
- `/` should not serve as a marketing homepage for normal browser entry.
- Unauthenticated `/` should resolve to the login contract, not `MulticaLanding`.
- If a landing page is retained for technical reasons, it must be explicitly classified as:
  - public marketing surface, or
  - compatibility seam only
  and must not be mistaken for the primary auth entry.

Current code reality (2026-05-15):
- `apps/web/app/(landing)/page.tsx` still renders `MulticaLanding`
- `apps/web/features/landing/components/redirect-if-authenticated.tsx` still exists and can route authenticated users onward

Operational conclusion:
- "homepage removed" is NOT yet a hard code fact.
- Future upgrade work must not assume landing was already deleted.

### A2. Onboarding contract
Target contract (Velafi self-host):
- Existing users should not be routed into `/onboarding` during normal login.
- `/onboarding` should not be part of the normal browser auth happy path for already-established users/workspaces.
- If onboarding remains in code for edge flows, it must be treated as a bounded fallback, not a routine destination.

Current code reality (2026-05-15):
- `apps/web/app/(auth)/onboarding/page.tsx` is a live route and renders `OnboardingFlow`
- `apps/web/app/auth/oidc/callback/page.tsx` still uses `paths.onboarding()` when `!hasOnboarded`
- `packages/views/layout/use-dashboard-guard.ts` and `resolvePostAuthDestination` still encode `/onboarding` as a valid route for un-onboarded users

Operational conclusion:
- "onboarding removed" is NOT yet a hard code fact.
- Login regressions that land on `/onboarding` are currently possible whenever auth maps to a user row with `onboarded_at = NULL`.

### A3. Logout contract
Target contract:
- There must be exactly ONE full logout orchestrator for the web product.
- The final logged-out landing URL must be `/login?logged_out=1` if `/login` remains auto-OIDC.
- Store-level auth reset code must not perform its own browser navigation.
- Store-level auth reset code must not independently call backend logout when a higher-level logout flow already does that.

Current repo hardening direction (2026-05-15):
- `packages/views/auth/use-logout.ts` should be the single full logout orchestrator
- `packages/core/auth/store.ts::logout()` should be state-reset only

### A4. OIDC user-matching contract
Target contract:
- Existing authorized users must reliably match one of:
  1. `(external_provider, external_user_id)`
  2. fallback by normalized email
- New-user creation must be an intentional policy outcome, not an accident caused by missing bindings.
- If signup is disabled, the failure must be understood as a data/identity mismatch, not misdiagnosed as a generic auth outage.

Current backend logic (confirmed 2026-05-15):
- `auth_oidc.go` precedence is external identity -> email -> create -> signup gate
- `auth.go::checkSignupAllowed()` blocks new-user creation when `ALLOW_SIGNUP=false` unless allowlists match

---

## B. Files that must be reviewed on every upgrade

### B1. Auth / logout / callback
- `apps/web/app/(auth)/login/page.tsx`
- `apps/web/app/auth/oidc/callback/page.tsx`
- `apps/web/components/web-providers.tsx`
- `packages/core/platform/auth-initializer.tsx`
- `packages/views/auth/use-logout.ts`
- `packages/core/auth/store.ts`
- `server/internal/handler/auth_oidc.go`
- `server/internal/handler/auth.go`

### B2. Homepage / landing / onboarding
- `apps/web/app/(landing)/page.tsx`
- `apps/web/features/landing/components/redirect-if-authenticated.tsx`
- `apps/web/app/(auth)/onboarding/page.tsx`
- `packages/views/layout/use-dashboard-guard.ts`
- `packages/core/paths/*`
- any changelog/i18n strings that still describe onboarding as a normal user path

---

## C. Mandatory pre-upgrade questions
Before any auth-adjacent upgrade, answer these explicitly in the ops note:

1. Is `/` supposed to remain a landing page, or become login-first only?
2. Is `/onboarding` still a supported browser route for this deployment?
3. Is logout meant to be local-only (`/auth/logout` + logged_out marker) or upstream-invalidation-based (`server-logout` chain)?
4. Which file is the single logout orchestrator?
5. Is `ALLOW_SIGNUP=false` compatible with the users expected to log in after cutover?
6. Which users in the target environment still lack `external_provider/external_user_id` bindings?

If any answer is unknown, do not call the auth surface "ready".

---

## D. Mandatory post-upgrade verification gates
These are REQUIRED after every auth-adjacent upgrade.

### D1. Browser gates
1. `/login`
   - expected: enters Authentik/Lark SSO flow
2. `/login?logged_out=1`
   - expected: stays local, does NOT auto-redirect to OIDC
3. `/auth/oidc/callback?code=...`
   - expected: one successful exchange only; no callback replay loop
4. app-internal `Sign out`
   - expected: ends at logged-out contract, does not silently bounce back in
5. existing-user login
   - expected: lands in workspace, not `/onboarding`
6. invitee / non-primary-user login (if supported)
   - expected: deterministic, documented route

### D2. Backend log gates
During one real login/logout run, capture:
- `POST /auth/oidc` status
- `GET /api/me` status before and after login
- `POST /auth/logout` status
- whether any immediate second `POST /auth/oidc` occurs after logout

### D3. Data-state gates
At minimum inspect:
```sql
select email, external_provider, external_user_id, onboarded_at
from "user"
order by updated_at desc
limit 30;
```

If login lands on `/onboarding` or 403s with signup disabled, compare the actual login identity against this table before blaming frontend routing.

---

## E. Current known entry points and hazards (2026-05-15)

### E1. Homepage still live
- `apps/web/app/(landing)/page.tsx` still serves `MulticaLanding`
- hazard: user expectation "homepage removed" is not yet enforced by code

### E2. Onboarding still live
- `apps/web/app/(auth)/onboarding/page.tsx` still renders onboarding flow
- `apps/web/app/auth/oidc/callback/page.tsx` still sends `!hasOnboarded` users to `/onboarding`
- hazard: wrong/misaligned user binding can surface onboarding even for users who believe they are fully established

### E3. Split logout was a real recurrence cause
- historical issue: `useLogout()` and store-level `logout()` both performed logout/navigation side effects
- fix direction: only one full orchestrator; state reset stays side-effect-light

### E4. Signup-disabled environments amplify identity mismatches
- with `ALLOW_SIGNUP=false`, a missing external binding does not create a user silently
- instead it fails loudly at `/auth/oidc` with 403
- this is good policy behavior, but bad observability if claims/match branch are not logged

---

## F. Recommended permanent hardening tasks
These are the actual anti-repeat items.

1. Delete or hard-redirect the landing page if product intent is login-first.
2. Remove `/onboarding` from the normal browser login decision tree for established self-host users.
3. Keep logout orchestration in exactly one place.
4. Add a dedicated auth contract test suite covering:
   - `/login`
   - `/login?logged_out=1`
   - callback one-shot behavior
   - existing-user OIDC login with signup disabled
   - app-internal logout
5. Add backend structured logs for OIDC matching branch:
   - external identity
   - email fallback
   - create blocked by signup gate
6. Maintain an environment audit query for users missing external bindings.

---

## G. Current operating rule for future upgrades
Do NOT say "we already fixed this last time" unless all three are true:
1. the code path was deleted or single-pointed,
2. a regression test exists and passes,
3. the current target environment data state still satisfies the same assumptions.

If any of the three is false, treat it as a live risk, not a solved lesson.
