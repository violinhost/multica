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
 * Should we ask this already-onboarded user where they heard about
 * Multica?
 *
 * Returns true for users who:
 *  - have completed onboarding (`onboarded_at` set), and
 *  - have not recorded any source (empty array or absent), and
 *  - did not previously click Skip on the onboarding `source` step, and
 *  - have not closed this backfill prompt enough times to dismiss it.
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
