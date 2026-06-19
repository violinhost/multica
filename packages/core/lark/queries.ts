import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

/** Query key namespace for everything Lark-installation-related. Realtime
 * sync invalidates `installations(wsId)` on `lark_installation:*` events
 * so the Settings panel updates without a refetch. */
export const larkKeys = {
  all: (wsId: string) => ["lark", wsId] as const,
  installations: (wsId: string) => [...larkKeys.all(wsId), "installations"] as const,
  // velafi-lark-inbox-pack: fallback inbox-notifier agent setting.
  inboxNotifier: (wsId: string) => [...larkKeys.all(wsId), "inbox-notifier"] as const,
};

export const larkInstallationsOptions = (wsId: string) =>
  queryOptions({
    queryKey: larkKeys.installations(wsId),
    queryFn: () => api.listLarkInstallations(wsId),
    enabled: !!wsId,
  });

// velafi-lark-inbox-pack: current workspace fallback inbox-notifier agent.
export const larkInboxNotifierOptions = (wsId: string) =>
  queryOptions({
    queryKey: larkKeys.inboxNotifier(wsId),
    queryFn: () => api.getLarkInboxNotifier(wsId),
    enabled: !!wsId,
  });
