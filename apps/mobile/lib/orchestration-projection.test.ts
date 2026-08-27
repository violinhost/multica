import type { OrchestrationProjection } from "@multica/core/types";
import { describe, expect, it } from "vitest";

import { orchestrationProjectionCardDetails } from "./orchestration-projection";

const projection = {
  schema_version: 1,
  producer: "automultica",
  receipt_id: "receipt-1",
  receipt_digest: "digest-1",
  workflow_id: "workflow-1",
  stage: "execution",
  role: "worker",
  substate: "running",
  reason_code: "work_started",
  since: "2026-08-27T00:00:00Z",
  elapsed_seconds: 0,
  sla_posture: "within_sla",
  route_generation: 1,
  native_status: { key: "todo", category: "todo", definition_id: "status-1" },
  next_action: { code: "await_completion" },
} satisfies OrchestrationProjection;

describe("orchestrationProjectionCardDetails", () => {
  it("returns null when an issue has no projection", () => {
    expect(orchestrationProjectionCardDetails(undefined)).toBeNull();
  });

  it("uses only receipt-bound projection fields for the card", () => {
    expect(orchestrationProjectionCardDetails(projection)).toEqual({
      title: "execution · worker",
      state: "running",
      reason: "work_started",
      nextAction: "await_completion",
    });
  });
});
