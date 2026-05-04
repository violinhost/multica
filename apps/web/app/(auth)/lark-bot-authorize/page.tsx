"use client";

/**
 * Velafi fork: /lark-bot-authorize?s=<session_id>
 *
 * Entered from a Lark applink that the customer-bot sends in DM. The user
 * lands inside the Lark in-app webview, already SSO-authenticated to
 * Multica via Phase A. They click [授权]; we POST to /api/lark-bot/authorize
 * which mints a PAT scoped to "lark-bot-<session>" and stamps the
 * lark_bot_session row. The bot polls /api/lark-bot/poll/:id, picks up
 * the plaintext, and continues in the Lark DM.
 *
 * If the user is not yet logged in (rare — Phase A SSO usually carries
 * them), we bounce through /login?next=… and come back authenticated.
 */

import { Suspense, useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { workspaceKeys } from "@multica/core/workspace/queries";
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
import { Loader2, CheckCircle2, AlertCircle } from "lucide-react";

type AuthState = { user: { email: string } | null; isLoading: boolean };
type Phase = "idle" | "submitting" | "success" | "error";

function LarkBotAuthorizeInner() {
  const searchParams = useSearchParams();
  const sessionId = searchParams.get("s") ?? "";

  const user = useAuthStore((s: AuthState) => s.user);
  const isLoading = useAuthStore((s: AuthState) => s.isLoading);
  const workspacesQuery = useQuery({
    queryKey: workspaceKeys.list(),
    queryFn: () => api.listWorkspaces(),
    enabled: !!user,
  });
  const workspaces: Workspace[] = workspacesQuery.data ?? [];

  const [phase, setPhase] = useState<Phase>("idle");
  const [errMsg, setErrMsg] = useState<string>("");

  // Bounce to /login if no session.
  useEffect(() => {
    if (isLoading) return;
    if (user) return;
    const here = `/lark-bot-authorize?s=${encodeURIComponent(sessionId)}`;
    window.location.href = `/login?next=${encodeURIComponent(here)}`;
  }, [isLoading, user, sessionId]);

  if (isLoading || !user || workspacesQuery.isLoading) {
    return <CenteredSpinner />;
  }

  if (!sessionId) {
    return (
      <CenteredCard
        title="Missing session"
        description="This authorization link is incomplete. Return to your Lark conversation and try again."
        icon={<AlertCircle className="h-10 w-10 text-destructive" />}
      />
    );
  }

  // Pick the first workspace the user belongs to. The bot creates the
  // session against this workspace; for Velafi we assume single tenant.
  const targetWorkspace = workspaces[0];
  if (!targetWorkspace) {
    return (
      <CenteredCard
        title="No workspace"
        description="Your account is not yet a member of any Multica workspace. Ask an owner to add you, then retry."
        icon={<AlertCircle className="h-10 w-10 text-destructive" />}
      />
    );
  }

  const submit = async () => {
    setPhase("submitting");
    setErrMsg("");
    try {
      const res = await fetch("/api/lark-bot/authorize", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          session_id: sessionId,
          workspace_id: targetWorkspace.id,
        }),
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { error?: string };
        throw new Error(body.error || `HTTP ${res.status}`);
      }
      setPhase("success");
    } catch (err) {
      setPhase("error");
      setErrMsg(err instanceof Error ? err.message : String(err));
    }
  };

  if (phase === "success") {
    return (
      <CenteredCard
        title="授权完成"
        description="回到 Lark 对话，Multica Project Agent 现在可以以你的身份建 issue / 查 issue 了。可以关闭此窗口。"
        icon={<CheckCircle2 className="h-10 w-10 text-success" />}
      />
    );
  }

  if (phase === "error") {
    return (
      <CenteredCard
        title="授权失败"
        description={
          errMsg ||
          "Authorization could not be completed. Try again from your Lark conversation."
        }
        icon={<AlertCircle className="h-10 w-10 text-destructive" />}
        action={
          <Button variant="outline" onClick={submit}>
            重试
          </Button>
        }
      />
    );
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>授权 Multica Project Agent</CardTitle>
          <CardDescription>
            Multica Project Agent 申请以你的身份在 Multica 中创建 / 查询 / 跟进 issue。
            授权后随时可以在 Settings → API Tokens 撤销。
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="rounded-md border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
            <div>登录账号: {user.email}</div>
            <div>工作区: {targetWorkspace.name}</div>
          </div>
          <Button
            type="button"
            className="w-full"
            disabled={phase === "submitting"}
            onClick={submit}
          >
            {phase === "submitting" ? (
              <>
                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                授权中...
              </>
            ) : (
              "授权"
            )}
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}

function CenteredSpinner() {
  return (
    <div className="flex min-h-screen items-center justify-center">
      <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
    </div>
  );
}

function CenteredCard({
  title,
  description,
  icon,
  action,
}: {
  title: string;
  description: string;
  icon: React.ReactNode;
  action?: React.ReactNode;
}) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <Card className="w-full max-w-sm">
        <CardHeader className="items-center text-center">
          <div className="mb-2 flex justify-center">{icon}</div>
          <CardTitle>{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </CardHeader>
        {action ? (
          <CardContent className="flex justify-center">{action}</CardContent>
        ) : null}
      </Card>
    </div>
  );
}

export default function LarkBotAuthorizePage() {
  return (
    <Suspense fallback={<CenteredSpinner />}>
      <LarkBotAuthorizeInner />
    </Suspense>
  );
}
