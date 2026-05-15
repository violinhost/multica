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

describe("useLogout", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiLogoutMock.mockResolvedValue(undefined);
  });

  it("calls server logout and routes to logged_out login URL", async () => {
    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useLogout(), { wrapper });

    await result.current();

    await waitFor(() => {
      expect(apiLogoutMock).toHaveBeenCalledTimes(1);
      expect(authLogoutMock).toHaveBeenCalledTimes(1);
      expect(pushMock).toHaveBeenCalledWith("/login?logged_out=1");
    });
  });

  it("still clears local state and routes to logged_out URL when server logout fails", async () => {
    apiLogoutMock.mockRejectedValueOnce(new Error("network fail"));
    const { wrapper } = createWrapper();
    const { result } = renderHook(() => useLogout(), { wrapper });

    await result.current();

    await waitFor(() => {
      expect(apiLogoutMock).toHaveBeenCalledTimes(1);
      expect(authLogoutMock).toHaveBeenCalledTimes(1);
      expect(pushMock).toHaveBeenCalledWith("/login?logged_out=1");
    });
  });
});
