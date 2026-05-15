"use client";

import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useAuthStore, markLogoutInProgress } from "@multica/core/auth";
import { api } from "@multica/core/api";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { clearWorkspaceStorage, defaultStorage } from "@multica/core/platform";
import { paths } from "@multica/core/paths";
import type { Workspace } from "@multica/core/types";
import { useNavigation } from "../navigation";

/**
 * Performs a complete logout: clears per-workspace client storage, legacy
 * cookies, the desktop tab state, the entire React Query cache, the
 * in-memory auth store, and finally navigates to /login. Wraps what was
 * previously duplicated in app-sidebar's logout handler so NoAccessPage's
 * "Sign in as a different user" and any future entry point can use the
 * same flow.
 *
 * Without a unified logout, callers that only do `navigate('/login')`
 * leave the auth cookie + React Query cache + local storage intact —
 * AuthInitializer then silently re-authenticates the user on the login
 * page and redirects them back where they came from.
 */
export function useLogout() {
  const queryClient = useQueryClient();
  const authLogout = useAuthStore((s) => s.logout);
  const { push } = useNavigation();

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

    // Logout should land on a stable logged-out login URL that suppresses
    // the login page's normal auto-OIDC redirect, otherwise an active upstream
    // SSO session can bounce the user straight back into the app.
    push(`${paths.login()}?logged_out=1`);
  }, [queryClient, authLogout, push]);
}
