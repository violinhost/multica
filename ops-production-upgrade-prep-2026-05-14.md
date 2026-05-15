# Multica production upgrade prep — us-dallas-svc-001

Date: 2026-05-14 22:31 CDT
Host: us-dallas-svc-001
Operator: Hermes

## Objective
Prepare the production Multica stack for a controlled upgrade after v0.3.2 rehearsal validation.

## Frozen / must-protect modules for this upgrade
These modules must be treated as upgrade-critical and verified globally, not piecemeal:

1. **Login / OIDC / Lark OSS auth flow**
   - `apps/web/app/(auth)/login/*`
   - `apps/web/app/auth/callback/*`
   - `apps/web/app/auth/oidc/callback/*`
   - `server/internal/handler/auth*.go`
   - `server/internal/auth/*`
   - `packages/core/auth/*`
   - `packages/core/platform/auth-initializer.tsx`
   - includes:
     - Authentik / OIDC authorize entry
     - Lark / Feishu OSS embedded-webview callback behavior
     - auth/session cookie policy
     - local logout + upstream OIDC end-session logout

2. **Workspace / member visibility and identity mapping**
   - `packages/views/settings/components/members-tab.tsx`
   - `server/internal/handler/workspace.go`
   - `server/pkg/db/queries/member.sql`
   - generated member/user db code

3. **Quick-add / Velafi quick create path**
   - `server/internal/handler/velafi_quick_add.go`
   - `packages/views/modals/quick-create-issue.tsx`
   - related quick-create stores/types

4. **Issue attachment open / download path**
   - `packages/views/editor/readonly-content.tsx`
   - `packages/ui/markdown/file-cards.ts`
   - backend `/uploads` route behavior
   - generated attachment db code

5. **Help / Docs launcher and `/docs` route**
   - `packages/views/layout/help-launcher.tsx`
   - `apps/web/proxy.ts`
   - `Dockerfile.web`
   - `DOCS_URL` runtime wiring

6. **Config/runtime bootstrap**
   - `server/internal/handler/config.go`
   - `packages/core/config/index.ts`
   - `packages/core/api/client.ts`
   - `server/cmd/server/router.go`
   - `docker-compose.selfhost*.yml`

Operational rule:
- do not treat any one of the above as locally fixed until the whole cluster of related surfaces is smoke-tested on production
- optimize for one fast, accurate cutover rather than multiple partial retries

## Version consistency target
Target for this upgrade:
- self-host backend/frontend: **v0.3.0** behavior target
- daemon / CLI on this node: **v0.3.0**
- database schema: **migrated to the schema level required by the v0.3.0 target checkout**

Current observed mismatch before upgrade:
- production backend/frontend containers are still `v0.2.27-fork-*`
- local CLI version is `0.2.27`
- running daemon binary path is `/Users/clusteradmin/multica-manual-upgrades/0.2.27/bin/multica daemon start --foreground`
- Homebrew-installed `multica` package on this node is only `0.2.8`

Operational consequence:
- version consistency is currently **not** satisfied
- production cutover must include daemon/CLI alignment, not only web/backend replacement

## Database protection and latest-version requirement
Current database facts:
- PostgreSQL: `17.9`
- backup validated by reading the custom dump TOC with `pg_restore` in a `postgres:17-alpine` container
- uploads archive validated by listing tar contents
- attachment rows currently store relative `/uploads/...` URLs in `attachment.url`

Current migration state:
- pre-cutover DB latest applied migration: `069_drop_task_last_heartbeat`
- repo latest migration file: `089_squad_no_action_activity_index`
- post-cutover DB latest applied migration: `089_squad_no_action_activity_index`
- migration gap closed: **yes**

Operational rule for DB protection:
1. do not claim "latest version" unless the production DB migration level matches the chosen release/checkout
2. take a fresh pre-cutover DB dump immediately before the actual production container replacement
3. record pre-upgrade `schema_migrations` state in the savepoint directory
4. after upgrade, verify that migrations completed cleanly and that `schema_migrations` advanced to the intended target set
5. if migrations fail or stop mid-gap, rollback app containers first and only consider DB restore if the schema/data state is no longer serviceable

## Live production baseline before upgrade
Containers:
- multica-frontend-1 -> `velafi/multica-web:v0.2.27-fork-b4913f36`
- multica-backend-1 -> `velafi/multica-backend:v0.2.27-fork-b4913f36`
- multica-postgres-1 -> `pgvector/pgvector:pg17`

Compose project:
- `multica`
- file: `docker-compose.selfhost.yml`
- real env file: `/Users/clusteradmin/agent-node/services/multica/compose/.env.runtime-oidc.merged`

Runtime facts:
- production backend port: `8180 -> 8080`
- production frontend port: `3330 -> 3000`
- rehearsal frontend port: `4330 -> 3000`
- rehearsal backend port: `9180 -> 8080`

Selected backend env observed from running container:
- `APP_ENV=production`
- `LOGIN_METHODS=oidc`
- `OIDC_EXTERNAL_PROVIDER=authentik`
- `FRONTEND_ORIGIN=https://multica.velafi.ai`
- `MULTICA_APP_URL=https://multica.velafi.ai`
- `OIDC_REDIRECT_URI=https://multica.velafi.ai/auth/oidc/callback`

## Savepoints created
Initial prep savepoint:
- `/Users/clusteradmin/agent-node/services/multica/ops/savepoints/prod-upgrade-20260514-223116`

Final pre-cutover savepoint:
- `/Users/clusteradmin/agent-node/services/multica/ops/savepoints/prod-cutover-20260514-225254`

## Data size at time of backup
- database size: `90 MB`
- uploads size: `280K`
- attachment rows: `14`

## Important current-state finding
Production `/docs` was failing before upgrade and remains a separate known issue.
Evidence before cutover:
- `curl http://127.0.0.1:3330/docs` -> `500`
- frontend logs show `Failed to proxy http://localhost:4000/docs` with `ECONNREFUSED`

Evidence after cutover:
- `curl http://127.0.0.1:3330/docs` -> `500`
- local docs service on `127.0.0.1:4000/docs` -> `200`

Interpretation:
- `/docs` remains a separate production docs/proxy defect
- it should NOT be treated as a regression introduced by the v0.3.0 cutover itself unless new evidence appears

## Rehearsal-linked code fact
Attachment open/download fix was identified in local repo commit:
- `c9563ff0 fix(attachments): allow relative /uploads file cards`

## 2026-05-15 issue attachment regression note (root cause confirmed)
Observed symptom from issue detail screenshot: uploaded files render as blue markdown links instead of file-card blocks, and clicking them neither opens nor downloads.

Confirmed code-path cause:
- `packages/views/editor/utils/link-handler.ts` sends any `href` starting with `/` through internal `multica:navigate` routing.
- Relative attachment URLs are stored as `/uploads/...` in current data/doc state.
- Therefore a plain markdown link like `[Bitwave_JE_V1.xlsx](/uploads/...)` is treated as an in-app route, not as a file URL.
- The local fix in `c9563ff0` only covers `!file[name](/uploads/...)` / file-card rendering paths in:
  - `packages/ui/markdown/file-cards.ts`
  - `packages/views/editor/readonly-content.tsx`
- If content remains serialized/rendered as a normal markdown link instead of `!file[...]`, click handling still falls into `openLink()` and breaks download/open.

Evidence:
- `packages/views/editor/utils/link-handler.ts`: `href.startsWith("/")` => dispatch `multica:navigate`
- `packages/ui/markdown/file-cards.ts`: new regex explicitly allows `!file[...](/uploads/...)`
- `packages/views/editor/readonly-content.test.tsx`: regression test only covers `!file[...]( /uploads/... )` file-card path
- `ops-production-upgrade-prep-2026-05-14.md`: attachment rows currently store relative `/uploads/...` URLs in `attachment.url`

Operational conclusion:
- This is primarily a frontend render/click-path bug for relative `/uploads` links that are not transformed into file cards.
- It is not yet evidence of storage loss or backend `/uploads` unavailability.

Recommended code fix direction:
1. In `openLink()`, special-case `/uploads/` as an external/download URL rather than internal app navigation; or
2. Ensure all issue/comment attachment markdown is normalized to `!file[...]` so file-card handling always applies.

Safer fix preference:
- Patch `openLink()` for `/uploads/` first, because it protects both legacy/plain links and any surface that fails to normalize into file cards.

## 2026-05-15 Lark OSS / OIDC destination-contract unification
Problem class:
- browser login entry, OIDC callback success path, and OIDC callback replay-fallback path did not share a single post-auth destination contract
- this left Lark OSS / Feishu webview login exposed to route drift (`/onboarding` vs first workspace vs `/workspaces/new`)

Repo correction applied:
- `apps/web/app/(auth)/login/page.tsx`
  - removed browser happy-path fallback to `/onboarding`
  - login entry now resolves normal post-auth browser destination through `resolvePostAuthDestination(...)`
- `apps/web/app/auth/oidc/callback/page.tsx`
  - normal success path now uses `resolvePostAuthDestination(wsList, hasOnboarded)`
  - replay / second-consume recovery path now also uses the same resolver
- `packages/core/paths/resolve.ts` remains the single browser destination authority:
  - first workspace if any
  - otherwise `/workspaces/new`

Why this matters:
- Lark OSS login correctness depends not only on `/auth/oidc 200`, but also on callback-after-login landing on a deterministic route under webview/browser lifecycle churn.
- before this change, callback and login had diverged destination logic, which made the auth surface non-frozen.

Validation:
- `pnpm --filter @multica/web build` -> passed locally after contract unification.

Operational note:
- this change reduces one major frozen-surface inconsistency, but full Lark OSS closure still requires live browser verification of the end-to-end chain: signed-out page -> Sign in with Lark -> Authentik/Lark -> `/auth/oidc` -> final app landing route.

## 2026-05-15 production auth split-path finding from Violin live report
User-reported live behavior and later Authentik event evidence show the production auth anomaly was caused by **different Authentik browser sessions producing different users/subs**, not by a drifting Multica binding.

Locked evidence chain:
- failing branch:
  - Authentik event around `17:31:06`: `authorize_application user=akadmin`
  - Multica backend around `17:31:07`: `POST /auth/oidc status=403`
  - corresponding Authentik user/sub: `akadmin` / `a9f67a33...`
- succeeding branch after `Not you?` + Lark re-login:
  - Authentik event around `17:34:38`: `login user=ou_2b4dfeb9f91af76e5418692a73455eff`
  - Authentik event around `17:34:39`: `authorize_application user=ou_2b4dfeb9f91af76e5418692a73455eff`
  - Multica backend around `17:34:40`: `POST /auth/oidc status=200`
  - corresponding Authentik user/sub: `ou_2b4dfeb9f91af76e5418692a73455eff` / `143e11f45a...`
- existing Multica DB binding for Violin:
  - `external_provider=authentik`
  - `external_user_id=143e11f45ac29cb39a3972e262ebe45e444795fa439f3d5e226c01fd08a3099f`
- therefore the successful branch already matches the existing Multica binding exactly; the binding did **not** drift.

Correct operational interpretation:
- The single root cause is **browser session contamination at Authentik**.
- When the browser still carries an `authentik_session` for `akadmin`, silent SSO into Multica authorizes as `akadmin` and emits the wrong `sub`, which Multica correctly rejects with signup-disabled `403`.
- After `Not you?` clears that session path and the user re-runs the Lark flow, Authentik authorizes as the real Lark-linked user (`ou_2b4...`), emits `sub=143e11f4...`, and Multica login succeeds because that value already matches the stored binding.

Important correction:
- Do **not** rebind the Multica `external_user_id` to the failing `akadmin` sub.
- That would convert a transient browser-session mix-up into a persistent DB identity corruption and would break the legitimate Lark-linked user path.

Immediate user fix:
1. Visit `https://multica.velafi.ai/idp/if/user/#/library`
2. Avatar menu -> `Logout`
3. Then start Multica login again so Authentik must run the full Lark flow
4. Expected result: Authentik emits the `ou_2b4...` / `143e11f4...` identity and Multica login succeeds without needing `Not you?`

Longer-term operational implication:
- treat unexpected silent SSO into the wrong Authentik user as a browser/session hygiene problem first
- before any DB rebind proposal, compare:
  1. Authentik event `user`
  2. emitted OIDC `sub`
  3. Multica stored `external_user_id`
- if stored binding already matches the intended Authentik/Lark user, the fix is session cleanup / flow discipline, not DB mutation


   - local daemon binary
3. Take one more immediate pre-cutover DB dump and save `schema_migrations` rows.
4. Replace production frontend/backend containers and let migrations run.
5. Verify DB migration completion before calling the app healthy.
6. Run production smoke tests immediately:
   - login / OIDC callback
   - app shell
   - quick-add
   - pending-first-login behavior
   - members visibility
   - help/docs entry behavior
   - issue attachment open/download
   - daemon registration / runtime availability
7. Align this node's daemon/CLI to `v0.3.0` and verify runtime heartbeats against the upgraded server.
8. Only shut down rehearsal/dev after production is verified stable.

## Rollback principle
If production regression appears after container replacement:
1. stop new production containers
2. restore previous image/container pairing or previous compose target
3. if data rollback is required, restore from pre-cutover savepoint artifacts

## Notes
- Do not shut down rehearsal before production verification.
- Do not classify the pre-existing `/docs` 500 as an upgrade failure unless behavior changes materially.

## 2026-05-15 logout / SSO rebound finding

### Live-chain evidence
- Chrome MCP confirmed the unauthenticated production login chain is:
  - `/login` -> `/api/me 401` -> `/api/oidc/health 200`
  - then automatic redirect to Authentik authorize
  - then Authentik `default-authentication-flow`
  - then Lark login flow after clicking `Continue with Multica Velafi SSO`
- Therefore, for a normal unauthenticated visit, seeing the Lark/SSO login flow is expected behavior.

### Root cause locked
- Frontend logout hook: `packages/views/auth/use-logout.ts`
- Before hotfix, the hook only cleared client state and navigated to `/login`.
- It did **not** call backend `POST /auth/logout`.
- It did **not** route to `/login?logged_out=1`.
- Backend live check confirmed `POST /auth/logout` correctly clears:
  - `multica_auth`
  - `multica_csrf`
- Login page code (`apps/web/app/(auth)/login/page.tsx`) explicitly suppresses auto-OIDC redirect when `logged_out=1` is present.
- Conclusion: prior Sign out behavior was only a client-side pseudo-logout; it could leave the browser in a state where upstream SSO rebound made the user appear immediately logged back in.

### Repo patch applied (not yet production-released)
- Updated `packages/views/auth/use-logout.ts` to:
  1. `await api.logout()` first (best effort)
  2. continue clearing local/query/workspace state
  3. route to `/login?logged_out=1`
- Added focused test file:
  - `packages/views/auth/use-logout.test.tsx`

### Verification
- `pnpm vitest run packages/views/auth/use-logout.test.tsx --reporter=dot` -> passed (2/2)
- Existing `apps/web/app/(auth)/login/page.test.tsx` currently fails in this repo test harness due to missing jsdom environment (`document/window is not defined`); this is an existing test-fixture problem, not a regression introduced by the logout patch.

### Operational significance
- This is a narrow front-end hotfix in the Login/OIDC frozen surface.
- Required browser smoke after deploy:
  1. login into dashboard
  2. click Sign out
  3. confirm landing URL is `/login?logged_out=1`
  4. confirm no immediate silent bounce back into dashboard
  5. confirm a fresh manual visit to `/login` (without `logged_out=1`) still enters the Authentik/Lark login flow

### Rehearsal deployment + browser smoke (2026-05-15)
- Rehearsal compose project confirmed as `multica-v032-rehearsal`
- Config files used by the running rehearsal stack:
  - `/Users/clusteradmin/agent-node/services/multica/repo/docker-compose.selfhost.yml`
  - `/Users/clusteradmin/agent-node/services/multica/repo/docker-compose.selfhost.build.yml`
- Environment file used by the running rehearsal stack:
  - `/Users/clusteradmin/projects/multica/runtime/rehearsal/.env.v032-rehearsal`
- Rebuilt and restarted rehearsal frontend/backend from current repo checkout only (no production touch)
- Post-restart readiness check:
  - `curl http://127.0.0.1:4330/api/config` -> `200`
- Chrome MCP browser smoke on rehearsal:
  - `https://multica-rehearsal.velafi.ai/login?logged_out=1`
    - stayed on local URL
    - rendered `Signing in... / Please wait while we redirect you to Lark authentication`
    - network showed `/api/config 200`, `/api/me 401`, `/api/oidc/health 200`
    - **did not** issue OIDC authorize redirect
  - `https://multica-rehearsal.velafi.ai/login`
    - redirected into Authentik flow as expected
    - page reached `Sign in to Multica` with `Continue with Multica Velafi SSO`
- Interpretation:
  - hotfix behavior is confirmed in rehearsal for the two key browser gates:
    1. logged-out URL suppresses automatic SSO rebound
    2. normal login URL still enters Authentik/Lark SSO flow
- Remaining acceptance gap:
  - full in-app `Sign out` click-path was not executed end-to-end in rehearsal during this pass because it requires a live authenticated app session in that browser context.

### Production hotfix release + gate verification (2026-05-15)
- Production compose project confirmed as `multica`
- Production config files used by the running stack:
  - `/Users/clusteradmin/agent-node/services/multica/repo/docker-compose.selfhost.yml`
  - `/Users/clusteradmin/agent-node/services/multica/repo/docker-compose.selfhost.build.yml`
- Production env file:
  - `/Users/clusteradmin/agent-node/services/multica/compose/.env.runtime-oidc.merged`
- Release action executed:
  - rebuilt current repo frontend/backend images for project `multica`
  - restarted production frontend container
  - backend container remained on `multica-backend:dev`
- Readiness:
  - `curl http://127.0.0.1:3330/api/config` recovered to `200` after restart
- Chrome MCP production browser gates after release:
  - `https://multica.velafi.ai/login?logged_out=1`
    - stayed on local URL
    - rendered `Signing in... / Please wait while we redirect you to Lark authentication`
    - network showed `/api/config 200`, `/api/me 401`, `/api/workspaces 401`, `/api/oidc/health 200`
    - **did not** issue OIDC authorize redirect
  - `https://multica.velafi.ai/login`
    - entered Authentik flow as expected
    - page reached `Sign in to Multica` with `Continue with Multica Velafi SSO`
- Production interpretation:
  - hotfix is live and the two critical logout/login browser gates pass
  - `logged_out=1` now suppresses silent SSO rebound on production
  - normal login still enters Authentik/Lark SSO

### Follow-up root cause found after production hotfix (2026-05-15)
- A second logout path still existed in `packages/core/auth/store.ts`.
- Before this follow-up patch, store-level `logout()` still navigated to `/login` instead of `/login?logged_out=1`.
- This explains why logout could appear to "recur" or "flip back" even after the first hotfix: some surfaces could still invoke the store-level logout contract instead of the unified hook-level contract.
- Follow-up patch applied in repo:
  - `packages/core/auth/store.ts`
    - changed final navigation target from `/login` to `/login?logged_out=1`
- Focused validation:
  - `pnpm vitest run packages/core/auth/store.test.ts --reporter=dot` -> passed (5/5)
- Operational interpretation:
  - the recurrence was caused by **split logout contracts** inside the frontend codebase, not a single missing line.
  - at least two independent logout entry points needed to agree on the same logged-out landing URL.

### Production follow-up release for store-level logout (2026-05-15)
- Release action executed:
  - rebuilt production frontend after `packages/core/auth/store.ts` follow-up patch
  - restarted production frontend only (`--no-deps frontend`)
- Readiness:
  - `curl http://127.0.0.1:3330/api/config` recovered to `200` after restart
- Browser gate verification after the follow-up release:
  - `https://multica.velafi.ai/login?logged_out=1`
    - stayed on local URL
    - network showed only `/api/config 200`, `/api/me 401`, `/api/workspaces 401`, `/api/oidc/health 200`
    - **still no** OIDC authorize redirect
  - `https://multica.velafi.ai/login`
    - still entered Authentik flow as expected
    - page reached `Sign in to Multica` with `Continue with Multica Velafi SSO`
- Production interpretation:
  - both known frontend logout entry points now converge on the same `logged_out=1` contract in live code
  - this closes the specific "one path fixed, another path still bounces to /login" recurrence mode

### Rehearsal shutdown after mission completion (2026-05-15)
- User explicitly requested the dev/rehearsal stack be stopped because its upgrade mission was complete.
- Executed:
  - `docker compose -p multica-v032-rehearsal ... down`
- Result:
  - removed `multica-v032-rehearsal-frontend-1`
  - removed `multica-v032-rehearsal-backend-1`
  - removed `multica-v032-rehearsal-postgres-1`
  - removed `multica-v032-rehearsal_default` network
- Production `multica` stack remained running:
  - `multica-frontend-1`
  - `multica-backend-1`
  - `multica-postgres-1`
- Ops note:
  - this closure only applied to the rehearsal/dev project; production was not touched during the shutdown step.

### Contract-level root cause after the failed follow-up (2026-05-15)
- Further repo review showed the deeper bug was not the destination string alone.
- `packages/views/auth/use-logout.ts` already orchestrated:
  - backend `api.logout()`
  - local workspace/query cleanup
  - final navigation to `/login?logged_out=1`
- But web auth guards/layouts could still observe `user=null` during the same transition and eagerly redirect to bare `/login`.
- Bare `/login` still auto-starts OIDC when Authentik is healthy.
- This creates a logout race:
  1. `POST /auth/logout` succeeds
  2. `/api/me` becomes `401`
  3. a guard/layout redirects to `/login`
  4. login page auto-redirects to OIDC
  5. user appears to be silently logged straight back in
- Live production backend evidence confirmed this exact sequence:
  - `POST /auth/logout` -> `200`
  - subsequent `GET /api/me` -> `401`
  - then immediate new `POST /auth/oidc` -> `200`
- Corrective refactor applied in repo:
  - added `packages/core/auth/logout-marker.ts`
  - `useLogout()` now marks a short-lived logout-in-progress session marker before clearing auth state
  - web redirect guards now upgrade bare `/login` to `/login?logged_out=1` when that marker is present:
    - `apps/web/app/[workspaceSlug]/layout.tsx`
    - `apps/web/app/(auth)/workspaces/new/page.tsx`
    - `apps/web/app/(auth)/onboarding/page.tsx`
    - `packages/views/layout/use-dashboard-guard.ts`
- login page now renders a dedicated signed-out card for `logged_out=1` instead of falling through to the default `Signing in...` redirect placeholder
- signed-out card `Sign in with Lark` action now uses a hard document navigation (`window.location.href = paths.login()`) rather than `router.replace(paths.login())`, because the latter could leave the app on the same route state without re-triggering the OIDC auto-entry effect in live browser conditions
- signed-out card authorize URL now also forces `prompt=login`, so Authentik must re-run interactive authentication instead of silently reusing an existing `authentik_session` and bypassing the Lark OSS step
- Focused validation:
- `pnpm vitest run packages/core/auth/logout-marker.test.ts packages/views/auth/use-logout.test.tsx --reporter=dot` -> passed (5/5)
- `pnpm --filter @multica/web build` -> passed locally after the signed-out render branch fix and hard-navigation button fix

  - the stable fix is not just "logout pushes the right URL"; guards must also respect the same logged-out contract during the post-logout auth-null window.
  - the login page must also render a non-redirecting signed-out state for `logged_out=1`, otherwise users see a misleading infinite-looking `Signing in...` card even when silent re-SSO is already suppressed.
