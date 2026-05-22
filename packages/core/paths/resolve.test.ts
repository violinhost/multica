import { describe, expect, it } from "vitest";
import type { Workspace } from "../types";
import { paths } from "./paths";
import { resolvePostAuthDestination } from "./resolve";

function makeWs(slug: string): Workspace {
  return {
    id: `id-${slug}`,
    name: slug,
    slug,
    description: null,
    context: null,
    settings: {},
    repos: [],
    issue_prefix: slug.toUpperCase(),
    created_at: "",
    updated_at: "",
  };
}

describe("resolvePostAuthDestination", () => {
  it("!onboarded → /workspaces/new or first workspace (NEVER /onboarding)", () => {
    // Velafi (2026-04-25): force-skip-onboarding — the onboarding
    // questionnaire is upstream lead-gen / segmentation UX, not relevant
    // to self-host. OIDC sign-ups skip it (auth_oidc.go MarkUserOnboarded),
    // and the resolver MUST NOT route to /onboarding either — workspace-
    // or-create-workspace path only. Fork-pack-A markers enforce this in
    // packages/core/paths/resolve.ts ("intentionally excluded from the
    // normal browser" comment).
    expect(resolvePostAuthDestination([], false)).toBe(paths.newWorkspace());
    expect(resolvePostAuthDestination([makeWs("acme")], false)).toBe(
      paths.workspace("acme").issues(),
    );
  });

  it("onboarded + has workspace → /<first.slug>/issues", () => {
    const ws = [makeWs("acme"), makeWs("beta")];
    expect(resolvePostAuthDestination(ws, true)).toBe(
      paths.workspace("acme").issues(),
    );
  });

  it("onboarded + zero workspaces → /workspaces/new", () => {
    expect(resolvePostAuthDestination([], true)).toBe(paths.newWorkspace());
  });
});
