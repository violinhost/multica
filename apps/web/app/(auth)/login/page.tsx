"use client";

import { Suspense, useEffect, useState } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { sanitizeNextUrl, useAuthStore } from "@multica/core/auth";
import { useConfigStore } from "@multica/core/config";
import { workspaceKeys } from "@multica/core/workspace/queries";
import {
  paths,
  resolvePostAuthDestination,
  useHasOnboarded,
} from "@multica/core/paths";
import { api } from "@multica/core/api";
import type { Workspace } from "@multica/core/types";
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
} from "@multica/ui/components/ui/card";
import { Button } from "@multica/ui/components/ui/button";
import { Loader2 } from "lucide-react";
import { setLoggedInCookie } from "@/features/auth/auth-cookie";
import { LoginPage, validateCliCallback } from "@multica/views/auth";
import { CliConfirm } from "./cli-confirm";

/**
 * Velafi /login: redirect-only OIDC entry point.
 *
 * Default behaviour: redirect any unauthenticated visitor straight to the
 * Authentik authorize endpoint — no email form, no SSO button, no UI to
 * maintain. The user sees a brief spinner, then Authentik. After OIDC
 * succeeds, the callback page sets the cookie and routes the user to the
 * post-auth destination.
 *
 * Three branches deviate from the default:
 *
 *   1. ?cli_callback=… (with a valid localhost / RFC-1918 host) — the
 *      multica CLI / daemon-bootstrap loopback flow. If the user is
 *      already authenticated we render <CliConfirm/> for explicit
 *      consent; otherwise we redirect to OIDC with the cli_callback URL
 *      preserved in the OIDC state's `next:` directive, so the post-auth
 *      callback brings the user back here logged in and CliConfirm picks up.
 *
 *   2. ?force=email — rescue path for when Authentik is down or admins
 *      need the legacy email verify-code flow. Renders the upstream
 *      <LoginPage/> with no `oidc` prop, so it shows email + Continue
 *      only. Backend gating on LOGIN_METHODS=oidc rejects /auth/send-code
 *      anyway, so ops must also flip LOGIN_METHODS to enable email
 *      backend before this rescue can succeed (deliberate two-key
 *      activation).
 *
 *   3. ?platform=desktop (without cli_callback) and the user already
 *      has a session — mints a CLI token and hands off via multica://
 *      deep link, just like the original implementation.
 *
 * The fork delta vs upstream: this file is a full rewrite (delete-vs-modify
 * conflict on rebase, easy to resolve by keeping ours), and packages/views
 * LoginPage stays upstream-pristine. Per FORK_MAINTENANCE.md §1 this is the
 * lowest-conflict-surface arrangement we found.
 */

async function resolveLoggedInDestination(
  qc: QueryClient,
  hasOnboarded: boolean,
  workspaces: Workspace[],
): Promise<string> {
  if (!hasOnboarded) {
    try {
      const invites = await api.listMyInvitations();
      if (invites.length > 0) {
        qc.setQueryData(workspaceKeys.myInvitations(), invites);
        return paths.invitations();
      }
    } catch {
      /* non-fatal — fall through */
    }
  }
  return resolvePostAuthDestination(workspaces, hasOnboarded);
}

function buildOIDCAuthorizeURL(
  authorizationEndpoint: string,
  clientID: string,
  redirectUri: string,
  state: string | undefined,
): string {
  const params = new URLSearchParams({
    client_id: clientID,
    redirect_uri: redirectUri,
    response_type: "code",
    scope: "openid email profile",
  });
  if (state) params.set("state", state);
  const sep = authorizationEndpoint.includes("?") ? "&" : "?";
  return `${authorizationEndpoint}${sep}${params}`;
}

function LoginPageContent() {
  const router = useRouter();
  const qc = useQueryClient();
  const oidcAuthorizationEndpoint = useConfigStore(
    (s) => s.oidcAuthorizationEndpoint,
  );
  const oidcClientID = useConfigStore((s) => s.oidcClientID);
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);
  const searchParams = useSearchParams();
  const hasOnboarded = useHasOnboarded();

  const cliCallbackRaw = searchParams.get("cli_callback");
  const cliState = searchParams.get("cli_state") || "";
  const platform = searchParams.get("platform");
  const forceEmail = searchParams.get("force") === "email";
  const cliPath =
    cliCallbackRaw != null && validateCliCallback(cliCallbackRaw);
  const isDesktopHandoff = platform === "desktop" && !cliPath;
  const nextUrl = sanitizeNextUrl(searchParams.get("next"));

  const [desktopToken, setDesktopToken] = useState<string | null>(null);
  const [desktopError, setDesktopError] = useState("");

  // Already authenticated: route to destination. Skip when CLI confirm is
  // active (CliConfirm handles its own action) or in forceEmail rescue.
  useEffect(() => {
    if (isLoading || !user || cliPath || forceEmail) return;
    if (isDesktopHandoff) {
      api
        .issueCliToken()
        .then(({ token }) => {
          setDesktopToken(token);
          window.location.href = `multica://auth/callback?token=${encodeURIComponent(token)}`;
        })
        .catch((err) => {
          setDesktopError(
            err instanceof Error
              ? err.message
              : "Failed to prepare Desktop sign-in",
          );
        });
      return;
    }
    if (nextUrl) {
      router.replace(nextUrl);
      return;
    }
    const list = qc.getQueryData<Workspace[]>(workspaceKeys.list()) ?? [];
    void resolveLoggedInDestination(qc, hasOnboarded, list).then((dest) =>
      router.replace(dest),
    );
  }, [
    isLoading,
    user,
    router,
    nextUrl,
    cliPath,
    isDesktopHandoff,
    hasOnboarded,
    qc,
    forceEmail,
  ]);

  // Unauthenticated → redirect to OIDC. Covers default visit, logout
  // bounce, browser-back from Authentik. Waits until OIDC config is
  // loaded (oidcAuthorizationEndpoint is empty until /api/config returns).
  useEffect(() => {
    if (
      isLoading ||
      user ||
      forceEmail ||
      !oidcAuthorizationEndpoint ||
      !oidcClientID
    ) {
      return;
    }

    // Race breaker: when the user clicks logout, auth-cookie.ts sets a
    // sessionStorage flag and navigates to Authentik's invalidation flow.
    // The auth store's set({user:null}) triggers this same effect to fire
    // before the navigation commits — without the flag, we'd race the
    // invalidation navigation and silently re-login via still-valid IDP
    // session.
    //
    // Two clearing conditions, whichever comes first:
    //   1. Referrer is the IDP path — we're back from Authentik invalidation
    //      and ready to re-auth (no IDP session anymore). Drop the flag and
    //      let the redirect proceed.
    //   2. Flag age > 1.5 s — covers the immediate React re-render race
    //      window (~10 ms) with margin, expires well before the post-
    //      invalidation bounce typically lands (~3 s) so we don't strand
    //      the user on a spinner if invalidation is fast.
    try {
      const stamp = sessionStorage.getItem("velafi-logout-in-progress");
      if (stamp) {
        const fromIDP =
          typeof document !== "undefined" &&
          document.referrer &&
          document.referrer.includes("/idp/");
        if (fromIDP) {
          sessionStorage.removeItem("velafi-logout-in-progress");
          /* fall through — re-auth normally */
        } else {
          const age = Date.now() - parseInt(stamp, 10);
          if (Number.isFinite(age) && age >= 0 && age < 1_500) {
            return;
          }
          sessionStorage.removeItem("velafi-logout-in-progress");
        }
      }
    } catch {
      /* sessionStorage unavailable — proceed without the race breaker */
    }

    let oidcState: string | undefined;
    if (cliPath && cliCallbackRaw) {
      // Preserve CLI flow through OIDC: after auth the callback page
      // navigates to `next:` which brings the user back to /login with
      // cli_callback intact, where CliConfirm finishes the loopback.
      const cliReturn = `/login?cli_callback=${encodeURIComponent(cliCallbackRaw)}&cli_state=${encodeURIComponent(cliState)}`;
      oidcState = `next:${cliReturn}`;
    } else {
      oidcState =
        [
          platform === "desktop" ? "platform:desktop" : "",
          nextUrl ? `next:${nextUrl}` : "",
        ]
          .filter(Boolean)
          .join(",") || undefined;
    }
    window.location.href = buildOIDCAuthorizeURL(
      oidcAuthorizationEndpoint,
      oidcClientID,
      `${window.location.origin}/auth/oidc/callback`,
      oidcState,
    );
  }, [
    isLoading,
    user,
    forceEmail,
    oidcAuthorizationEndpoint,
    oidcClientID,
    cliPath,
    cliCallbackRaw,
    cliState,
    platform,
    nextUrl,
  ]);

  // Desktop handoff display while a token is being minted.
  if (isDesktopHandoff && user) {
    if (desktopError) {
      return (
        <div className="flex min-h-screen items-center justify-center">
          <Card className="w-full max-w-sm">
            <CardHeader className="text-center">
              <CardTitle className="text-2xl">Sign-in Failed</CardTitle>
              <CardDescription>{desktopError}</CardDescription>
            </CardHeader>
          </Card>
        </div>
      );
    }
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            <CardTitle className="text-2xl">Opening Multica</CardTitle>
            <CardDescription>
              {desktopToken
                ? "You should see a prompt to open the Multica desktop app. If nothing happens, click the button below."
                : "Preparing Desktop sign-in..."}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex justify-center">
            {desktopToken ? (
              <Button
                variant="outline"
                onClick={() => {
                  window.location.href = `multica://auth/callback?token=${encodeURIComponent(desktopToken)}`;
                }}
              >
                Open Multica Desktop
              </Button>
            ) : (
              <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            )}
          </CardContent>
        </Card>
      </div>
    );
  }

  // CLI confirm path: cli_callback validated AND user authed.
  if (cliPath && cliCallbackRaw && user) {
    return (
      <CliConfirm
        cliCallback={cliCallbackRaw}
        cliState={cliState}
        onTokenObtained={setLoggedInCookie}
      />
    );
  }

  // Force-email rescue: render upstream LoginPage in email-only mode (no
  // oidc prop). Backend /auth/send-code rejects with 403 when
  // LOGIN_METHODS=oidc, so this rescue only succeeds after ops also flips
  // LOGIN_METHODS to include "email". Two-key activation by design.
  if (forceEmail) {
    return (
      <LoginPage
        onSuccess={async () => {
          const list = qc.getQueryData<Workspace[]>(workspaceKeys.list()) ?? [];
          const onboarded =
            useAuthStore.getState().user?.onboarded_at != null;
          if (nextUrl) {
            router.push(nextUrl);
            return;
          }
          const dest = await resolveLoggedInDestination(qc, onboarded, list);
          router.push(dest);
        }}
        onTokenObtained={setLoggedInCookie}
      />
    );
  }

  // Default + CLI-without-session: spinner while either /api/config
  // resolves (so we know the OIDC authorize URL) or the redirect effect
  // above fires.
  return (
    <div className="flex min-h-screen items-center justify-center">
      <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
    </div>
  );
}

export default function Page() {
  return (
    <Suspense fallback={null}>
      <LoginPageContent />
    </Suspense>
  );
}
