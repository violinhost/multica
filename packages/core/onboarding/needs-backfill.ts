import type { User } from "../types";

/**
 * Maximum number of times the user can close the backfill prompt with
 * the X / ESC / outside-click before we treat it as a permanent
 * dismissal. After that the prompt stops appearing.
 *
 * Submit and explicit Skip are always terminal (they write to the
 * server). The count exists only for the "I'll think about it later"
 * close path — without a cap, a user who never decides would see the
 * prompt every login forever.
 */
export const SOURCE_BACKFILL_MAX_DISMISSALS = 3;

/**
 * Minimum number of issues completed by an AI assignee (agent or
 * squad) in the current workspace before the source prompt may open.
 *
 * Source is not asked during onboarding at all — attribution is a
 * zero-payoff question for the user, so we wait until Multica has
 * demonstrably delivered value (agents finished real work) before
 * spending goodwill on it. Answer rates for "how did you hear about
 * us" prompts are also materially better after an activation moment
 * than at signup. Three completed issues are enough evidence that an
 * engaged new user has experienced the core workflow.
 *
 * The count itself comes from `agentCompletedIssueCountOptions` in
 * `./queries.ts`; the modal combines it with `needsSourceBackfill`.
 */
export const SOURCE_BACKFILL_MIN_AGENT_DONE_ISSUES = 3;

/**
 * Should we ask this already-onboarded user where they heard about
 * Multica?
 *
 * Returns true for users who:
 *  - have completed onboarding (`onboarded_at` set), and
 *  - have not recorded any source (empty array or absent), and
 *  - did not previously decline the source question (skip marker), and
 *  - have not closed this backfill prompt enough times to dismiss it.
 *
 * This is the user-level half of the gate. The workspace-level half —
 * "have agents completed at least SOURCE_BACKFILL_MIN_AGENT_DONE_ISSUES
 * issues here?" — needs a server query, so it lives in the modal
 * (`source-backfill-modal.tsx`), which also uses this predicate to
 * decide whether that query is worth running at all.
 *
 * Pure function — `dismissCount` is passed in so this stays callable
 * from core (no localStorage / StorageAdapter dependency).
 */
export function needsSourceBackfill(
  _user: User | null | undefined,
  _dismissCount: number,
): boolean {
  // Velafi self-host fork (frozen DIY): suppress the upstream source-attribution
  // backfill prompt — "How did you hear about Multica?" (MUL-2796 / #3550).
  // Velafi members arrive via OIDC + directory quick-add, not acquisition
  // channels, so the prompt is pure noise; worse, it re-fires on every refresh
  // for our OIDC users (onboarded_at is set but questionnaire.source is never
  // recorded, so the upstream predicate stays true forever). Hard-off here at
  // the single core chokepoint both web + desktop call. Original upstream
  // predicate preserved in git history — KEEP this trim across upgrades.
  return false;
}
