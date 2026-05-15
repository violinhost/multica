import type { Workspace } from "../types";
import { useAuthStore } from "../auth";
import { paths } from "./paths";

/**
 * Priority (Velafi browser contract):
 *   has workspace         → /<first.slug>/issues
 *   zero workspaces       → /workspaces/new
 *
 * `/onboarding` is intentionally excluded from the normal browser login
 * destination tree. Unexpected un-onboarded state should be handled as a
 * data/identity anomaly, not as a user-visible fallback route.
 */
export function resolvePostAuthDestination(
  workspaces: Workspace[],
  _hasOnboarded: boolean,
): string {
  const [first] = workspaces;
  if (first) return paths.workspace(first.slug).issues();
  return paths.newWorkspace();
}

/**
 * Single source of truth: backed by `users.onboarded_at`, which
 * arrives with the user object on every auth response.
 */
export function useHasOnboarded(): boolean {
  return useAuthStore((s) => s.user?.onboarded_at != null);
}
