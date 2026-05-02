import { describe, expect, it } from "vitest";
import { NextRequest } from "next/server";
import { proxy } from "./proxy";

function makeRequest(path: string, cookieHeader?: string) {
  return new NextRequest(`https://multica.velafi.ai${path}`, {
    headers: cookieHeader ? { cookie: cookieHeader } : undefined,
  });
}

describe("web proxy root routing", () => {
  it("redirects unauthenticated root visits to /login", () => {
    const res = proxy(makeRequest("/"));
    expect(res.status).toBe(307);
    expect(res.headers.get("location")).toBe("https://multica.velafi.ai/login");
  });

  it("redirects authenticated root visits with last workspace cookie to workspace issues", () => {
    const res = proxy(
      makeRequest("/", "multica_logged_in=1; last_workspace_slug=velafi")
    );
    expect(res.status).toBe(307);
    expect(res.headers.get("location")).toBe(
      "https://multica.velafi.ai/velafi/issues"
    );
  });

  it("allows authenticated root visits without last workspace cookie to continue", () => {
    const res = proxy(makeRequest("/", "multica_logged_in=1"));
    expect(res.status).toBe(200);
  });
});
