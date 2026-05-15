# Bridge × Squad boundary reshaping proposal v1 (2026-05-13)

## Purpose
This note converts the current high-level Bridge × Squad judgment into an implementation-oriented boundary proposal for svc-001.

It assumes:
- Bridge already has live-proven control-plane value
- upstream Multica Squad exists conceptually upstream
- svc-001 has not yet upgraded to the native Squad-capable upstream version

Therefore this proposal is about **pre-adaptation**, not premature native integration claims.

---

## Executive conclusion
Bridge should **not** be abandoned.
Bridge should be **re-scoped**.

Target shape:
- Multica Squad owns more of the **team-entry / delegation abstraction**
- Bridge continues to own the **workflow control-plane / evidence / handoff / gate** layer

In one line:
> Squad may replace part of Bridge's organization-layer modeling, but it does not replace Bridge's workflow truth layer.

---

## A. Bridge responsibilities that can be weakened or deleted over time
These are the areas most likely to be partially absorbed by native Squad once upstream upgrade is real and stable.

### A1. Team-entry abstraction duplicated inside Bridge
If Bridge currently models role-routing in a way that effectively recreates a team-assignee concept, this should be reduced.

Examples of likely over-modeling:
- hard-coded role-entry identity that really means a team lane
- Bridge-local assumptions that one phase always maps to one single fixed agent identity
- custom roster-like conventions inside Bridge when upstream already has squad membership/leader semantics

Direction:
- stop deepening Bridge-owned org modeling unless it is required for current live continuity
- prefer compatibility with upstream assignee/team objects over bespoke Bridge team abstractions

### A2. PM-as-universal-triage abstraction
If PM_Agent is acting mainly as a synthetic stand-in for "team leader who only triages and delegates", part of that responsibility may later be better owned by squad leader semantics.

What may become reducible:
- PM-only for first-touch routing when no true PM judgment is needed
- Bridge logic that exists only to fake organization-level dispatch hierarchy

Caution:
- do not delete PM workflow authority prematurely
- only weaken the "org-triage shell" part, not the cross-stage workflow authority

### A3. Bridge-local roster conventions
If Bridge eventually stores or infers team/member structures just to choose which role gets the next task, prefer replacing that with:
- upstream squad membership
- leader delegation
- explicit assignee resolution from upstream

---

## B. Bridge responsibilities that should be preserved
These are the areas with current live-proven value and no evidence yet that upstream Squad replaces them.

### B1. Workflow state machine
Bridge should remain the authority for structured stage progression such as:
- PM -> Coding
- Coding -> QA
- QA -> PM / Done / Rework
- future Deploy / Feedback loops

Why keep it:
- Squad is currently a delegation primitive, not a demonstrated workflow graph engine

### B2. Structured result contracts
Bridge should keep contracts such as:
- `coding_result.v1`
- QA result payloads
- explicit accepted / rejected / replayable result semantics

Why keep it:
- comments and leader activity are not substitutes for machine-consumable completion contracts

### B3. Authoritative artifact and review surface
Bridge should keep ownership of:
- authoritative artifact generation/rebuild
- stable review URLs
- artifact overwrite discipline when fallback content is replaced by authoritative content

Why keep it:
- this is already a proven hard-value layer in the live Coding -> QA closure

### B4. Cross-stage handoff and gates
Bridge should keep:
- coding completion -> QA handoff logic
- QA pass/fail gate behavior
- future deploy/feedback gates
- stage transition auditability

Why keep it:
- leader delegation does not equal workflow gate enforcement

### B5. Parent/child truth synchronization
Bridge should keep:
- parent shadow truth
- child materialization truth
- replay / reconciliation behavior
- state visibility needed for ops debugging

Why keep it:
- this is where live operational evidence and recovery discipline currently resides

### B6. Runtime/operator evidence layer
Bridge should continue to expose and document:
- writeback acceptance/failure boundaries
- route materialization evidence
- artifact provenance
- watcher/dispatch/handoff operational truth

Why keep it:
- upstream team-routing primitives do not automatically provide ops-grade workflow diagnostics

---

## C. New squad-aware compatibility surface to add
These are the safest additions to make before real upstream upgrade.

### C1. Route target typing
Bridge route targets should become explicitly typed.

Recommended shape:
- `route_target_type = agent | squad`
- `route_target_id`
- `route_target_name` (optional display/helper field)

Current live behavior:
- may continue to use `agent` only

Reason:
- avoids future cold redesign when native squad assignment becomes available

### C2. Delegation metadata placeholders
Add optional fields that preserve future upstream delegation truth without requiring immediate live use.

Recommended optional metadata:
- `leader_agent_id`
- `delegated_member_id`
- `squad_name`
- `delegation_mode` (e.g. direct / squad_leader / manual)

Reason:
- preserves future evidence about who actually triaged vs who actually executed

### C3. Child/read-model awareness
Child task records should be able to distinguish:
- route assigned to agent directly
- route assigned to squad then delegated to member

Minimum compatibility intent:
- no need to force immediate DB/schema migration if not required today
- but new notes/tests/contracts should stop assuming all routes are single-agent direct ownership

### C4. Contract wording updates
Prompt / route / handoff wording should stop implicitly assuming the target is always an individual agent.

Preferred wording pattern:
- "route target"
- "executing member"
- "leader-mediated delegation"

Avoid wording that bakes in:
- one role == one direct executor forever

---

## D. Recommended pre-upgrade validation plan
This is the highest-value work that can be done before weekend upgrade.

### D1. Boundary matrix
Produce a responsibility table with columns:
- responsibility
- current owner (Bridge / upstream / mixed)
- keep / weaken / migrate / observe
- reason

### D2. Route contract review
Review current route payloads and identify where they assume:
- single agent ownership
- direct assignee semantics
- no delegation hop

Output:
- list of fields that need future `agent | squad` compatibility

### D3. Shadow simulation
Run one low-risk workflow where an existing coordinator role behaves like a squad leader:
- receives parent intent
- delegates downstream
- does not itself claim implementation output
- final state still reconciles via Bridge

Success condition:
- Bridge workflow truth remains coherent under leader-mediated topology

### D4. Upgrade-day proof plan
Prepare the exact minimum checklist for the weekend upgrade:
1. create/resolve squad object
2. assign issue to squad
3. verify leader wakeup
4. verify delegation to member
5. verify Bridge can still preserve structured handoff/writeback/gates over that path

---

## E. Decision rule
Until native Squad is upgraded and live-proven on svc-001:
- do not stop Bridge mainline
- do not over-invest in duplicating team abstractions inside Bridge
- do invest in squad-aware contract compatibility
- do preserve Bridge ownership of workflow truth and evidence layers

---

## F. Bottom line
Bridge should become **less responsible for who the team is**, and remain **fully responsible for what the workflow truth is**.
