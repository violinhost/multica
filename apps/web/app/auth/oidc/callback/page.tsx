"use client";

import { Suspense, useEffect, useRef, useState } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { useQueryClient } from "@tanstack/react-query";
import { sanitizeNextUrl, useAuthStore } from "@multica/core/auth";
import { useConfigStore } from "@multica/core/config";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { paths, useHasOnboarded, resolvePostAuthDestination } from "@multica/core/paths";
import { api } from "@multica/core/api";
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
} from "@multica/ui/components/ui/card";
import { Button } from "@multica/ui/components/ui/button";
import { Loader2 } from "lucide-react";

function CallbackContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const qc = useQueryClient();
  const loginWithOIDC = useAuthStore((s) => s.loginWithOIDC);
  const oidcRedirectURI = useConfigStore((s) => s.oidcRedirectURI);
  const hasOnboarded = useHasOnboarded();
  const [error, setError] = useState("");
  const [desktopToken, setDesktopToken] = useState<string | null>(null);
  const consumedRef = useRef(false);

  useEffect(() => {
    const code = searchParams.get("code");
    if (!code) {
      setError("Missing authorization code");
      return;
    }

    const errorParam = searchParams.get("error");
    if (errorParam) {
      setError(errorParam);
      return;
    }

    if (consumedRef.current) {
      return;
    }
    consumedRef.current = true;

    const state = searchParams.get("state") || "";
    const stateParts = state.split(",");
    const isDesktop = stateParts.includes("platform:desktop");
    const nextPart = stateParts.find((p) => p.startsWith("next:"));
    const nextUrl = sanitizeNextUrl(nextPart ? nextPart.slice(5) : null);

    const redirectUri =
      oidcRedirectURI || `${window.location.origin}/auth/oidc/callback`;

    if (isDesktop) {
      api
        .oidcLogin(code, redirectUri)
        .then(({ token }) => {
          if (typeof window !== "undefined") {
            window.history.replaceState({}, "", paths.login());
          }
          setDesktopToken(token);
          window.location.href = `multica://auth/callback?token=${encodeURIComponent(token)}`;
        })
        .catch((err) => {
          setError(err instanceof Error ? err.message : "Login failed");
        });
    } else {
      loginWithOIDC(code, redirectUri)
        .then(async () => {
          const wsList = await api.listWorkspaces();
          qc.setQueryData(workspaceKeys.list(), wsList);
          const defaultDest = resolvePostAuthDestination(wsList, hasOnboarded);
          if (typeof window !== "undefined") {
            window.history.replaceState({}, "", nextUrl || defaultDest);
          }
          router.replace(nextUrl || defaultDest);
        })
        .catch(async (err) => {
          // Defensive: a prior consume of the same `code` may have already
          // succeeded (set the auth cookie) and the current call failed
          // because the IDP rejected the second exchange. Mobile Lark/
          // Feishu webview lifecycle (background evict, bfcache restore,
          // network swap) can re-mount this page → fresh useRef → second
          // consume. If `getMe()` succeeds the user is in fact logged in;
          // navigate as if the call had succeeded instead of flashing a
          // misleading "Login Failed" UI.
          try {
            const me = await api.getMe();
            if (me) {
            const wsList = await api.listWorkspaces().catch(() => []);
            qc.setQueryData(workspaceKeys.list(), wsList);
            const dest = resolvePostAuthDestination(wsList, hasOnboarded);
            if (typeof window !== "undefined") {
              window.history.replaceState({}, "", nextUrl || dest);
            }
            router.replace(nextUrl || dest);
            return;
            }
          } catch {
            /* not authenticated — fall through and show the original error */
          }
          setError(err instanceof Error ? err.message : "Login failed");
        });
    }
  }, [searchParams, loginWithOIDC, oidcRedirectURI, router, qc]);

  if (desktopToken) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            <CardTitle className="text-2xl">Opening Multica</CardTitle>
            <CardDescription>
              You should see a prompt to open the Multica desktop app. If
              nothing happens, click the button below.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex justify-center">
            <Button
              variant="outline"
              onClick={() => {
                window.location.href = `multica://auth/callback?token=${encodeURIComponent(desktopToken)}`;
              }}
            >
              Open Multica Desktop
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            <CardTitle className="text-2xl">Login Failed</CardTitle>
            <CardDescription>{error}</CardDescription>
          </CardHeader>
          <CardContent className="flex justify-center">
            <a
              href={paths.login()}
              className="text-primary underline-offset-4 hover:underline"
            >
              Back to login
            </a>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen items-center justify-center">
      <Card className="w-full max-w-sm">
        <CardHeader className="text-center">
          <CardTitle className="text-2xl">Signing in...</CardTitle>
          <CardDescription>
            Please wait while we complete your login
          </CardDescription>
        </CardHeader>
        <CardContent className="flex justify-center">
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        </CardContent>
      </Card>
    </div>
  );
}

export default function CallbackPage() {
  return (
    <Suspense fallback={null}>
      <CallbackContent />
    </Suspense>
  );
}
