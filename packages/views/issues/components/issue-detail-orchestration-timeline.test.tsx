import { describe, expect, it } from "vitest";
import type { TimelineEntry } from "@multica/core/types";
import enIssues from "../../locales/en/issues.json";

import { formatActivity } from "./issue-detail";

const t = new Proxy(() => "", {
  apply: (_target, _thisArg, [selector]: [(messages: typeof enIssues) => string]) => selector(enIssues),
}) as unknown as Parameters<typeof formatActivity>[1];

const orchestrationEntry = {
  id: "activity-1",
  type: "activity",
  action: "orchestration_projection_updated",
  actor_type: "plugin",
  actor_id: "installation-1",
  created_at: "2026-08-27T12:00:00Z",
  details: {
    stage: "handoff",
    role: "Coding_Agent",
    substate: "materializing",
    reason_code: "awaiting_receipt",
    next_action_code: "await_run",
  },
} as unknown as TimelineEntry;

describe("issue detail orchestration timeline", () => {
  it("renders structured projection fields after native status is in_progress", () => {
    const text = formatActivity(orchestrationEntry, t, undefined, undefined);
    expect(text).toContain("handoff / Coding_Agent");
    expect(text).toContain("materializing (awaiting_receipt)");
    expect(text).toContain("next: await_run");
    expect(text).not.toContain("in_progress");
  });
});
