"use client";

import { useState } from "react";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import { Button } from "@multica/ui/components/ui/button";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";

/**
 * CLI authorization confirm page — extracted from the upstream LoginPage's
 * cli_confirm step so that Velafi's redirect-only /login can keep CLI auth
 * working without keeping the rest of the email-login UI in the parent
 * component.
 *
 * Flow:
 *   1. CLI binds local port, opens browser to /login?cli_callback=...&cli_state=...
 *   2. Parent /login route sees cli_callback + an authenticated user → renders
 *      this component. (No session → parent redirects through OIDC first,
 *      preserving cli_callback in the OIDC `next:` state, so the user lands
 *      back here logged in.)
 *   3. User clicks Authorize → /api/cli-token mints a fresh JWT → redirect
 *      to the CLI's localhost callback with token+state.
 *
 * Layer-3 security: explicit user click required — no drive-by token grants.
 * `validateCliCallback` upstream filters the callback URL host before
 * rendering this component.
 */
interface CliConfirmProps {
  /** Caller URL the CLI bound; already validated by parent. */
  cliCallback: string;
  /** Opaque CSRF state passed from CLI; echoed back in the redirect. */
  cliState: string;
  /** Called after a token is minted (e.g. to set the logged-in cookie). */
  onTokenObtained?: () => void;
}

function redirectToCliCallback(url: string, token: string, state: string) {
  const sep = url.includes("?") ? "&" : "?";
  window.location.href = `${url}${sep}token=${encodeURIComponent(token)}&state=${encodeURIComponent(state)}`;
}

export function CliConfirm({
  cliCallback,
  cliState,
  onTokenObtained,
}: CliConfirmProps) {
  const user = useAuthStore((s) => s.user);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  // Parent guards on `user` truthiness; defensive null check just in case.
  if (!user) return null;

  const handleAuthorize = async () => {
    setLoading(true);
    setError("");
    try {
      const { token } = await api.issueCliToken();
      onTokenObtained?.();
      redirectToCliCallback(cliCallback, token, cliState);
    } catch {
      setError("Failed to authorize CLI. Please log in again.");
      setLoading(false);
    }
  };

  const handleSwitchAccount = async () => {
    // Sign out then bounce to /login with cli_callback preserved. Parent
    // route will see no session + cli_callback → redirect through OIDC for
    // fresh auth.
    try {
      await api.logout();
    } catch {
      /* non-fatal */
    }
    api.setToken(null);
    localStorage.removeItem("multica_token");
    useAuthStore.setState({ user: null });
    window.location.href = `/login?cli_callback=${encodeURIComponent(cliCallback)}&cli_state=${encodeURIComponent(cliState)}`;
  };

  return (
    <div className="flex min-h-svh items-center justify-center">
      <Card className="w-full max-w-sm">
        <CardHeader className="text-center">
          <CardTitle className="text-2xl">Authorize CLI</CardTitle>
          <CardDescription>
            Allow the CLI to access Multica as{" "}
            <span className="font-medium text-foreground">{user.email}</span>?
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <Button
            onClick={handleAuthorize}
            disabled={loading}
            className="w-full"
            size="lg"
          >
            {loading ? "Authorizing..." : "Authorize"}
          </Button>
          <Button
            variant="ghost"
            className="w-full"
            onClick={handleSwitchAccount}
            disabled={loading}
          >
            Use a different account
          </Button>
          {error && (
            <p className="text-sm text-destructive text-center">{error}</p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
