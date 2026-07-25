#!/usr/bin/env bash
set -euo pipefail

wf=".github/workflows/w1-backend-canary.yml"

[ -f "$wf" ]
grep -q '^  workflow_dispatch:$' "$wf"
grep -q '^      head_sha:$' "$wf"
grep -q '^      base_sha:$' "$wf"
grep -q 'image: pgvector/pgvector:pg17' "$wf"
grep -q 'image: redis:7-alpine' "$wf"
grep -q 'MULTICA_REQUIRE_TEST_DB: "1"' "$wf"
grep -q 'MULTICA_REQUIRE_TEST_REDIS: "1"' "$wf"
grep -q 'go test -json -race ./...' "$wf"
grep -q 'TestCreateAgentTask_RunningOwnerGuardIsHeadScopedAndRerunSafe' "$wf"
grep -q 'TestCreateAgentTask_DispatchedToRunningTransitionRaceReturnsNoRows' "$wf"
grep -q 'TestRedisLocalSkillListStore_CreateGetComplete' "$wf"
grep -q 'substrate-backed tests skipped instead of running' "$wf"

echo "w1-backend-canary workflow contract verified"
