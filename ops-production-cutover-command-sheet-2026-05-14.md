# Multica production cutover command sheet — us-dallas-svc-001

## Scope
Target: upstream v0.3.0 baseline + local attachment patch c9563ff0
Real production env file: /Users/clusteradmin/agent-node/services/multica/compose/.env.runtime-oidc.merged
Repo: /Users/clusteradmin/agent-node/services/multica/repo

## Batch A — pre-cutover savepoint
```bash
export MULTICA_REPO=/Users/clusteradmin/agent-node/services/multica/repo
export MULTICA_ENV=/Users/clusteradmin/agent-node/services/multica/compose/.env.runtime-oidc.merged

ts=$(date +%Y%m%d-%H%M%S)
dir=/Users/clusteradmin/agent-node/services/multica/ops/savepoints/prod-cutover-$ts
mkdir -p "$dir"

docker exec multica-postgres-1 pg_dump -U multica -d multica -Fc > "$dir/multica-prod-precutover.pgdump"
docker exec multica-postgres-1 psql -U multica -d multica -Atc "select version, applied_at from schema_migrations order by version;" > "$dir/schema_migrations_before.txt"
docker inspect multica-frontend-1 multica-backend-1 multica-postgres-1 > "$dir/container_inspect_before.json"
docker logs --tail 300 multica-frontend-1 > "$dir/frontend_before.log" 2>&1
docker logs --tail 300 multica-backend-1 > "$dir/backend_before.log" 2>&1
```

## Batch B — build target images from current checkout
```bash
cd "$MULTICA_REPO"
git show --no-patch --decorate HEAD
git diff --name-status v0.3.0..HEAD

docker compose \
  --env-file "$MULTICA_ENV" \
  -f docker-compose.selfhost.yml \
  -f docker-compose.selfhost.build.yml \
  build backend frontend
```

## Batch C — production cutover (frontend/backend only)
```bash
cd "$MULTICA_REPO"
docker compose \
  --env-file "$MULTICA_ENV" \
  -f docker-compose.selfhost.yml \
  -f docker-compose.selfhost.build.yml \
  up -d backend frontend
```

## Batch D — immediate post-cutover checks
```bash
docker exec multica-postgres-1 psql -U multica -d multica -Atc "select count(*), max(version) from schema_migrations;"
curl -sS -o /tmp/prod-config.json -w 'config:%{http_code}\n' http://127.0.0.1:8180/api/config
curl -sS -o /dev/null -w 'root:%{http_code}\n' http://127.0.0.1:3330/
curl -sS -o /dev/null -w 'docs:%{http_code}\n' http://127.0.0.1:3330/docs
docker logs --tail 300 multica-backend-1
```

## Batch E — daemon / CLI alignment
```bash
multica update
multica --version
multica daemon restart
multica daemon status
which multica
ls -l /opt/homebrew/bin/multica
```

## Batch F — frozen module validation checklist
- login / OIDC callback
- members visibility
- pending-first-login badge
- Velafi quick-add
- help/docs launcher behavior
- issue attachment open/download
- daemon registration / runtime availability

## Rollback — app first, DB last
### Roll back app containers to previous production images
Use the previously running image references:
- frontend: velafi/multica-web:v0.2.27-fork-b4913f36
- backend: velafi/multica-backend:v0.2.27-fork-b4913f36

```bash
cd "$MULTICA_REPO"
MULTICA_WEB_IMAGE=velafi/multica-web \
MULTICA_BACKEND_IMAGE=velafi/multica-backend \
MULTICA_IMAGE_TAG=v0.2.27-fork-b4913f36 \
docker compose \
  --env-file "$MULTICA_ENV" \
  -f docker-compose.selfhost.yml \
  up -d backend frontend
```

### DB restore only if schema/data state is no longer serviceable
```bash
# example only; do not run unless explicitly needed
# drop connections / restore from latest prod-cutover savepoint dump
```
