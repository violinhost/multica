# Multica homepage/onboarding removal — minimal change plan

Date: 2026-05-15
Host: us-dallas-svc-001
Author: Hermes
Purpose: translate the user requirement "remove homepage / remove onboarding page" into an explicit code-change checklist.

## Target product contract
For Velafi self-host browser users:
- `/` is NOT a marketing homepage.
- `/` should resolve into the login-first auth contract.
- Existing users should NOT enter `/onboarding` during normal browser login.
- `/onboarding` should not remain a routine fallback destination in auth routing.

## Current live code entry points that still violate the target

### 1. Homepage / landing still exists
File:
- `apps/web/app/(landing)/page.tsx`

Current behavior:
- renders `MulticaLanding`
- mounts `RedirectIfAuthenticated`

Minimum change options:
- Option A (preferred): replace page body with a server/client redirect to `/login`
- Option B: delete the landing route and move root handling entirely into proxy/middleware

Required companion checks:
- `apps/web/proxy.ts`
- any references assuming authenticated `/` may fall through to landing
- metadata/SEO pages that only existed for landing should be reviewed as dead/secondary surfaces

### 2. Landing redirect helper still encodes onboarding as a valid destination
File:
- `apps/web/features/landing/components/redirect-if-authenticated.tsx`

Current behavior:
- uses `resolvePostAuthDestination(list, hasOnboarded)`
- comment explicitly states it may send users to `/onboarding`

Minimum change:
- stop using onboarding as a valid post-auth landing for the normal browser contract
- either:
  - call a Velafi-specific resolver with no onboarding branch, or
  - change the shared resolver contract if onboarding is globally retired for this deployment

### 3. OIDC callback still routes `!hasOnboarded` to `/onboarding`
File:
- `apps/web/app/auth/oidc/callback/page.tsx`

Current behavior:
- `defaultDest = !hasOnboarded ? paths.onboarding() : ...`
- fallback path in the catch branch does the same

Minimum change:
- replace `/onboarding` destination with the intended Velafi browser destination
- likely candidates:
  - first workspace issues page if memberships exist
  - `/workspaces/new` if zero memberships and that is the real desired first-run path
- important: do not leave callback success routing dependent on `onboarded_at` if onboarding is no longer a user-visible product flow

### 4. Onboarding route is still a live full page
File:
- `apps/web/app/(auth)/onboarding/page.tsx`

Current behavior:
- renders `OnboardingFlow`
- only bounces away when `hasOnboarded` is already true

Minimum change options:
- Option A (preferred): convert route to immediate redirect for web builds
- Option B: hard-block route outside explicitly allowed internal/dev flows
- Option C: leave the code in repo but remove it from all browser login success routing and add a guard page explaining the route is unsupported

### 5. Shared dashboard guard still considers onboarding a normal destination
File:
- `packages/views/layout/use-dashboard-guard.ts`

Current behavior:
- comment and behavior encode:
  - un-onboarded -> `/onboarding`

Minimum change:
- update the fallback resolver contract so dashboard guard no longer treats onboarding as a normal browser route

### 6. Shared path helpers still expose onboarding as a first-class global route
Files:
- `packages/core/paths/paths.ts`
- `packages/core/paths/*` resolver helpers

Current behavior:
- `paths.onboarding()` exists as a normal global route
- route classification still includes `/onboarding`

Minimum change:
- if onboarding is truly retired, path helpers should stop advertising it as a normal destination
- if kept only for legacy/internal reasons, annotate clearly and keep it out of normal auth resolution

## Required resolver change
There must be one explicit Velafi browser post-auth resolver that does NOT branch to onboarding.

Desired output contract should be defined explicitly, e.g.:
1. if user has memberships -> first/last workspace issues page
2. if user has zero memberships -> `/workspaces/new` (or another explicit first-run route)
3. never `/onboarding` for normal browser login

## Validation gates after implementing removal
1. unauthenticated `/` -> `/login`
2. authenticated existing user login -> workspace route, never `/onboarding`
3. direct `/onboarding` visit by existing user -> redirected away
4. callback success path -> never `/onboarding`
5. no tests or comments still describe onboarding as the default browser branch

## Why this matters
Without deleting these entry points from code, the requirement exists only as human memory. Any future merge/rebase can re-expose onboarding/homepage behavior because the old routes still exist and shared resolvers still know how to send users there.
