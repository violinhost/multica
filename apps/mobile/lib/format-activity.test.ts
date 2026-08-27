import type { TimelineEntry } from "@multica/core/types";
import { describe, expect, it } from "vitest";

import { formatActivity } from "./format-activity";

const baseEntry = {
  type: "activity" as const,
  id: "activity-1",
  actor_type: "agent",
  actor_id: "agent-1",
  created_at: "2026-08-27T00:00:00Z",
};

const resolveActorName = () => "Agent";

describe("formatActivity", () => {
  it("formats an orchestration projection update from its timeline payload", () => {
    const entry = {
      ...baseEntry,
      action: "orchestration_projection_updated",
      details: {
        stage: "execution",
        role: "worker",
        substate: "running",
        reason_code: "work_started",
        next_action_code: "await_completion",
      },
    } satisfies TimelineEntry;

    expect(formatActivity(entry, resolveActorName)).toBe(
      "updated orchestration: execution / worker — running (work_started); next: await_completion",
    );
  });

  it("does not render missing orchestration fields as undefined", () => {
    const entry = {
      ...baseEntry,
      action: "orchestration_projection_updated",
      details: {},
    } satisfies TimelineEntry;

    expect(formatActivity(entry, resolveActorName)).toBe(
      "updated orchestration",
    );
  });
});
