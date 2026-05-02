import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

function createWrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

const {
  mockIssueCliToken,
  mockLogout,
  searchParamsState,
  authStateRef,
  configStateRef,
} = vi.hoisted(() => ({
  mockIssueCliToken: vi.fn(),
  mockLogout: vi.fn(),
  searchParamsState: { params: new URLSearchParams() },
  authStateRef: {
    state: {
      sendCode: vi.fn(),
      verifyCode: vi.fn(),
      user: null as null | { id: string; email: string; onboarded_at?: string },
      isLoading: false,
    },
  },
  configStateRef: {
    state: {
      oidcAuthorizationEndpoint: "",
      oidcClientID: "",
    },
  },
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/login",
  useSearchParams: () => searchParamsState.params,
}));

vi.mock("@multica/core/auth", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/auth")>(
      "@multica/core/auth",
    );
  const useAuthStore = Object.assign(
    (selector: (s: typeof authStateRef.state) => unknown) =>
      selector(authStateRef.state),
    {
      getState: () => authStateRef.state,
      setState: (
        update: Partial<typeof authStateRef.state>,
      ) => {
        Object.assign(authStateRef.state, update);
      },
    },
  );
  return { ...actual, useAuthStore };
});

vi.mock("@multica/core/config", () => ({
  useConfigStore: (selector: (s: typeof configStateRef.state) => unknown) =>
    selector(configStateRef.state),
}));

vi.mock("@/features/auth/auth-cookie", () => ({
  setLoggedInCookie: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listWorkspaces: vi.fn().mockResolvedValue([]),
    listMyInvitations: vi.fn().mockResolvedValue([]),
    issueCliToken: mockIssueCliToken,
    logout: mockLogout,
    setToken: vi.fn(),
    getMe: vi.fn(),
  },
}));

import LoginPage from "./page";

describe("LoginPage (Velafi redirect-only)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    searchParamsState.params = new URLSearchParams();
    authStateRef.state.user = null;
    authStateRef.state.isLoading = false;
    configStateRef.state.oidcAuthorizationEndpoint = "";
    configStateRef.state.oidcClientID = "";
  });

  it("renders spinner before OIDC config has loaded", () => {
    render(<LoginPage />, { wrapper: createWrapper() });
    // No email form, no SSO button — only the redirect-pending spinner.
    expect(screen.queryByLabelText("Email")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /continue/i }),
    ).not.toBeInTheDocument();
  });

  it("redirects to OIDC authorize URL once config has loaded (default visit)", async () => {
    configStateRef.state.oidcAuthorizationEndpoint =
      "https://auth.example/application/o/authorize/";
    configStateRef.state.oidcClientID = "client-abc";

    const hrefSetter = vi.fn();
    const originalLocation = window.location;
    Object.defineProperty(window, "location", {
      configurable: true,
      value: {
        ...originalLocation,
        origin: "http://localhost",
        set href(value: string) {
          hrefSetter(value);
        },
      },
    });

    try {
      render(<LoginPage />, { wrapper: createWrapper() });
      await waitFor(() => {
        expect(hrefSetter).toHaveBeenCalledTimes(1);
      });
      const url: string = hrefSetter.mock.calls[0][0];
      expect(url).toContain(
        "https://auth.example/application/o/authorize/?",
      );
      expect(url).toContain("client_id=client-abc");
      expect(url).toContain("response_type=code");
      expect(url).toContain(
        "redirect_uri=http%3A%2F%2Flocalhost%2Fauth%2Foidc%2Fcallback",
      );
    } finally {
      Object.defineProperty(window, "location", {
        configurable: true,
        value: originalLocation,
      });
    }
  });

  it("encodes ?next= into OIDC state for post-auth redirect", async () => {
    searchParamsState.params = new URLSearchParams({ next: "/invite/abc" });
    configStateRef.state.oidcAuthorizationEndpoint =
      "https://auth.example/application/o/authorize/";
    configStateRef.state.oidcClientID = "client-abc";

    const hrefSetter = vi.fn();
    const originalLocation = window.location;
    Object.defineProperty(window, "location", {
      configurable: true,
      value: {
        ...originalLocation,
        origin: "http://localhost",
        set href(value: string) {
          hrefSetter(value);
        },
      },
    });

    try {
      render(<LoginPage />, { wrapper: createWrapper() });
      await waitFor(() => {
        expect(hrefSetter).toHaveBeenCalledTimes(1);
      });
      const url: string = hrefSetter.mock.calls[0][0];
      expect(decodeURIComponent(url)).toContain("state=next:/invite/abc");
    } finally {
      Object.defineProperty(window, "location", {
        configurable: true,
        value: originalLocation,
      });
    }
  });

  it("renders CLI confirm UI when ?cli_callback= valid AND user authed", async () => {
    searchParamsState.params = new URLSearchParams({
      cli_callback: "http://localhost:9876/cb",
      cli_state: "csrf-state",
    });
    authStateRef.state.user = {
      id: "u1",
      email: "violin@velafi.com",
    };

    render(<LoginPage />, { wrapper: createWrapper() });

    expect(
      await screen.findByText(/authorize cli/i),
    ).toBeInTheDocument();
    expect(screen.getByText(/violin@velafi\.com/)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /^authorize$/i }),
    ).toBeInTheDocument();
  });

  it("redirects to OIDC with cli_callback preserved when ?cli_callback= AND no session", async () => {
    searchParamsState.params = new URLSearchParams({
      cli_callback: "http://localhost:9876/cb",
      cli_state: "csrf-state",
    });
    configStateRef.state.oidcAuthorizationEndpoint =
      "https://auth.example/application/o/authorize/";
    configStateRef.state.oidcClientID = "client-abc";
    // user remains null

    const hrefSetter = vi.fn();
    const originalLocation = window.location;
    Object.defineProperty(window, "location", {
      configurable: true,
      value: {
        ...originalLocation,
        origin: "http://localhost",
        set href(value: string) {
          hrefSetter(value);
        },
      },
    });

    try {
      render(<LoginPage />, { wrapper: createWrapper() });
      await waitFor(() => {
        expect(hrefSetter).toHaveBeenCalledTimes(1);
      });
      const url: string = hrefSetter.mock.calls[0][0];
      // OIDC state encodes a /login?cli_callback=… return URL.
      // Outer URLSearchParams encoding decodes once via decodeURIComponent,
      // leaving the cli_callback URL still encoded (encodeURIComponent on
      // the inner URL is intentional — that survives the second decode the
      // browser performs when navigating back to /login).
      const decoded = decodeURIComponent(url);
      expect(decoded).toContain("state=next:/login?cli_callback=");
      expect(decoded).toContain("cli_state=csrf-state");
      // The inner URL is still pct-encoded inside the state param.
      expect(decoded).toContain("http%3A%2F%2Flocalhost%3A9876%2Fcb");
    } finally {
      Object.defineProperty(window, "location", {
        configurable: true,
        value: originalLocation,
      });
    }
  });

  it("renders email rescue form when ?force=email", () => {
    searchParamsState.params = new URLSearchParams({ force: "email" });

    render(<LoginPage />, { wrapper: createWrapper() });

    // Upstream LoginPage renders email + Continue (no oidc prop passed)
    expect(screen.getByText("Sign in to Multica")).toBeInTheDocument();
    expect(screen.getByLabelText("Email")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /^continue$/i }),
    ).toBeInTheDocument();
  });

  // Regression MUL-1080: desktop handoff continues to work.
  it("mints a token and deep-links to Desktop when authed + platform=desktop", async () => {
    searchParamsState.params = new URLSearchParams({ platform: "desktop" });
    authStateRef.state.user = {
      id: "u1",
      email: "test@multica.ai",
    };
    mockIssueCliToken.mockImplementation(() =>
      Promise.resolve({ token: "handoff-jwt" }),
    );

    const hrefSetter = vi.fn();
    const originalLocation = window.location;
    Object.defineProperty(window, "location", {
      configurable: true,
      value: {
        ...originalLocation,
        set href(value: string) {
          hrefSetter(value);
        },
      },
    });

    try {
      render(<LoginPage />, { wrapper: createWrapper() });

      await waitFor(() => {
        expect(mockIssueCliToken).toHaveBeenCalledTimes(1);
      });
      await waitFor(() => {
        expect(hrefSetter).toHaveBeenCalledWith(
          "multica://auth/callback?token=handoff-jwt",
        );
      });
      expect(
        await screen.findByRole("button", { name: "Open Multica Desktop" }),
      ).toBeInTheDocument();
    } finally {
      Object.defineProperty(window, "location", {
        configurable: true,
        value: originalLocation,
      });
    }
  });
});
