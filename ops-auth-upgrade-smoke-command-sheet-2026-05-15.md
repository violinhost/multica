# Multica auth/logout upgrade smoke command sheet

Date: 2026-05-15
Host: us-dallas-svc-001
Author: Hermes
Purpose: command-first smoke checklist so future upgrades do not rely on memory.

## 1. Runtime / env gates
```bash
docker ps --format 'table {{.Names}}\t{{.Image}}\t{{.Ports}}\t{{.Status}}' | egrep 'multica-(frontend|backend|postgres)-1|multica-v032-rehearsal-(frontend|backend|postgres)-1|NAMES'

docker exec multica-backend-1 /bin/sh -lc 'printf "ALLOW_SIGNUP=%s\nALLOWED_EMAILS=%s\nALLOWED_EMAIL_DOMAINS=%s\nOIDC_EXTERNAL_PROVIDER=%s\nLOGIN_METHODS=%s\n" "$ALLOW_SIGNUP" "$ALLOWED_EMAILS" "$ALLOWED_EMAIL_DOMAINS" "$OIDC_EXTERNAL_PROVIDER" "$LOGIN_METHODS"'
```

## 2. Basic HTTP gates
```bash
curl -sS -o /tmp/prod_config.json -w '%{http_code}\n' http://127.0.0.1:3330/api/config
curl -sS -o /tmp/rehearsal_config.json -w '%{http_code}\n' http://127.0.0.1:4330/api/config
curl -sS https://multica.velafi.ai/api/oidc/health
curl -sS https://multica-rehearsal.velafi.ai/api/oidc/health
```

## 3. Browser gates (manual / Chrome MCP)
Check all of these after auth-adjacent changes:
- `/login`
- `/login?logged_out=1`
- successful `/auth/oidc/callback`
- app-internal `Sign out`
- existing-user login landing route
- invitee / alternate user login route if relevant

Expected browser outcomes:
- `/login` -> Authentik/Lark flow
- `/login?logged_out=1` -> local logged-out page, no auto-OIDC
- successful existing-user login -> workspace route, not onboarding
- Sign out -> logged-out landing, no silent rebound

## 4. Backend log gates
```bash
docker logs --since 15m multica-backend-1 2>&1 | egrep 'POST /auth/logout|POST /auth/oidc|GET /api/me|GET /api/workspaces|user logged in via oidc|oidc claims received|oidc user match|oidc user match blocked' | tail -n 200

docker logs --since 15m multica-v032-rehearsal-backend-1 2>&1 | egrep 'POST /auth/logout|POST /auth/oidc|GET /api/me|GET /api/workspaces|user logged in via oidc|oidc claims received|oidc user match|oidc user match blocked' | tail -n 200
```

## 5. User binding / onboarding data gates
```bash
docker exec multica-postgres-1 psql -U multica -d multica -c "select email, coalesce(external_provider,'' ) as external_provider, coalesce(external_user_id,'' ) as external_user_id, onboarded_at, (select count(*) from member m where m.user_id=u.id) as memberships from \"user\" u order by updated_at desc limit 30;"

docker exec multica-v032-rehearsal-postgres-1 psql -U multica -d multica -c "select email, coalesce(external_provider,'' ) as external_provider, coalesce(external_user_id,'' ) as external_user_id, onboarded_at, (select count(*) from member m where m.user_id=u.id) as memberships from \"user\" u order by updated_at desc limit 30;"
```

Interpretation rules:
- `POST /auth/oidc 403` + `user registration is disabled...` means the login identity did not match an existing user/binding and new-user creation was blocked.
- login landing on `/onboarding` means the matched user row still has `onboarded_at = NULL` (or routing still uses onboarding as a normal destination).
- logout recurrence must not be diagnosed until you confirm which logout entry point ran and whether `/auth/oidc` fired again immediately after logout.

## 6. Release discipline
- Never change auth/logout/homepage/onboarding in production first.
- Rehearsal must pass browser gates before production.
- If production login becomes visibly worse, rollback frontend first before deeper analysis.
