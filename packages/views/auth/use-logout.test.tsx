/** @vitest-environment jsdom */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

const { pushMock, authLogoutMock, apiLogoutMock, removeItemMock } = vi.hoisted(
  () => ({
    pushMock: vi.fn(),
    authLogoutMock: vi.fn(),
    apiLogoutMock: vi.fn().mockResolvedValue(undefined),
    removeItemMock: vi.fn(),
  }),
);

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { logout: () => void }) => unknown) =>
    selector({ logout: authLogoutMock }),
  markLogoutInProgress: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    logout: apiLogoutMock,
  },
}));

vi.mock("@multica/core/workspace/queries", () => ({
  workspaceKeys: { list: () => ["workspaces"] },
}));

vi.mock("@multica/core/platform", () => ({
  clearWorkspaceStorage: vi.fn(),
  defaultStorage: {
    removeItem: removeItemMock,
  },
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push: pushMock }),
}));

import { useLogout } from "./use-logout";

function createWrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return {
    qc,
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    ),
  };
}

// Velafi (2026-05-15): web logout navigates via hard `window.location.href`
// to Authentik's `/idp/if/flow/default-invalidation-flow/` to kill the
// upstream authentik_session cookie. Tests below verify that behavior
// rather than the upstream `push("/login?logged_out=1")` flow.
describe("useLogout (Velafi Authentik invalidation flow)", () => {
  let locationHref: string;
  const realLocation = window.location;

  beforeEach(() => {
    vi.clearAllMocks();
    apiLogoutMock.mockResolvedValue(undefined);
    locationHref = "";
    Object.defineProperty(window, "location", {
      configurable: true,
      value: {
        ...realLocation,
        get href() {
          return locationHref;
        },
        set href(v: string) {
          locationHref = v;
        },
      },
    });
  });

  it("calls server logout and navigates to Authentik invalidation flow", async () => {
    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useLogout(), { wrapper });

    await result.current();

    await waitFor(() => {
      expect(apiLogoutMock).toHaveBeenCalledTimes(1);
      expect(authLogoutMock).toHaveBeenCalledTimes(1);
      expect(locationHref).toBe("/idp/if/flow/default-invalidation-flow/");
    });
  });

  it("still clears local state and navigates to Authentik when server logout fails", async () => {
    apiLogoutMock.mockRejectedValueOnce(new Error("network fail"));
    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useLogout(), { wrapper });

    await result.current();

    await waitFor(() => {
      expect(apiLogoutMock).toHaveBeenCalledTimes(1);
      expect(authLogoutMock).toHaveBeenCalledTimes(1);
      expect(locationHref).toBe("/idp/if/flow/default-invalidation-flow/");
    });
  });
});
