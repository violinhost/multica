"use client";

import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useAuthStore, markLogoutInProgress } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { clearWorkspaceStorage, defaultStorage } from "@multica/core/platform";
import type { Workspace } from "@multica/core/types";

/**
 * Performs a complete logout: clears per-workspace client storage, legacy
 * cookies, the desktop tab state, the entire React Query cache, the
 * in-memory auth store, and finally navigates to the Authentik invalidation
 * flow which kills the authentik_session cookie before redirecting back to
 * Multica's login. Without killing the upstream Authentik session, the next
 * login attempt fails with `FlowNonApplicableException` because Authentik
 * sees the user already authenticated as the same user the source returns
 * (incident reproduced 2026-05-15 second-login-cycle: Permission denied /
 * Flow does not apply to current user).
 *
 * The Authentik default-invalidation-flow runs:
 *   [0] UserLogoutStage (clears authentik_session, no UI)
 *   [10] velafi-invalidation-redirect (RedirectStage → https://multica.velafi.ai/)
 * which then lands on /login, where useEffect picks up the OIDC redirect.
 */
export function useLogout() {
  const queryClient = useQueryClient();
  const authLogout = useAuthStore((s) => s.logout);

  return useCallback(async () => {
    markLogoutInProgress();
    try {
      await api.logout();
    } catch {
      // Best effort: even if the server logout request fails, still clear
      // local client state so the user is not left on a protected route.
    }

    // Clear workspace-scoped storage for every workspace this user has
    // access to, BEFORE clearing the React Query cache (which holds the
    // workspace list). Otherwise per-workspace drafts/chat/etc would leak
    // to the next user on this device.
    const cachedWorkspaces =
      queryClient.getQueryData<Workspace[]>(workspaceKeys.list()) ?? [];
    for (const ws of cachedWorkspaces) {
      clearWorkspaceStorage(defaultStorage, ws.slug);
    }

    // Clear the last-workspace-slug cookie. Otherwise on a shared device
    // the next user gets redirected by the proxy to the previous user's
    // last workspace, then bounced to NoAccessPage — confusing.
    if (typeof document !== "undefined") {
      document.cookie =
        "last_workspace_slug=; path=/; max-age=0; SameSite=Lax";
    }

    // Clear desktop tab state. Tab paths can contain workspace slugs and
    // issue UUIDs that must not survive across user sessions on a shared
    // machine. No-op on web (web doesn't write this key).
    defaultStorage.removeItem("multica_tabs");

    queryClient.clear();
    authLogout();

    // Velafi (2026-05-15): hard navigation to Authentik invalidation flow.
    // This kills the authentik_session cookie BEFORE we land back on /login,
    // so the next OIDC authorize call sees no Authentik session and forces
    // a fresh source authentication (no FlowNonApplicableException).
    // Authentik's velafi-invalidation-redirect lands us back at
    // https://multica.velafi.ai/ which then 307s to /login, where useEffect
    // auto-redirects to OIDC.
    if (typeof window !== "undefined") {
      window.location.href = "/idp/if/flow/default-invalidation-flow/";
    }
  }, [queryClient, authLogout]);
}
