import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { OrchestrationProjection } from "@multica/core/types";

import { OrchestrationProjectionSummary } from "./orchestration-projection-summary";

const projection: OrchestrationProjection = {
  schema_version: 1,
  producer: "automultica",
  receipt_id: "receipt-1",
  receipt_digest: "digest-1",
  workflow_id: "workflow-1",
  stage: "handoff",
  role: "Coding_Agent",
  substate: "materializing",
  reason_code: "awaiting_receipt",
  since: "2026-08-27T12:00:00Z",
  elapsed_seconds: 42,
  sla_posture: "at_risk",
  route_generation: 7,
  native_status: { key: "in_progress", category: "in_progress", definition_id: "status-1" },
  next_action: { code: "await_run", target: "Coding_Agent" },
};

describe("OrchestrationProjectionSummary", () => {
  it("renders nothing when the optional projection is absent", () => {
    const { container } = render(<OrchestrationProjectionSummary projection={undefined} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders compact payload text with an accessible full label", () => {
    render(<OrchestrationProjectionSummary projection={projection} compact />);
    expect(screen.getByText("handoff · Coding_Agent · materializing")).toBeVisible();
    expect(screen.getByLabelText(/Orchestration: handoff, Coding_Agent, materializing/)).toBeVisible();
  });

  it("renders all full projection fields without consulting native status", () => {
    render(<OrchestrationProjectionSummary projection={projection} />);
    expect(screen.getByLabelText("Orchestration projection")).toHaveTextContent("handoff · Coding_Agent");
    expect(screen.getByText("materializing · awaiting_receipt")).toBeVisible();
    expect(screen.getByText("Elapsed 42s · SLA at_risk")).toBeVisible();
    expect(screen.getByText("Next: await_run → Coding_Agent")).toBeVisible();
    expect(screen.queryByText("in_progress")).not.toBeInTheDocument();
  });

  it("keeps a long compact payload in a bounded, truncatable element", () => {
    const long = { ...projection, stage: "stage-".repeat(80), role: "role-".repeat(80) };
    render(<OrchestrationProjectionSummary projection={long} compact />);
    const summary = screen.getByLabelText(/^Orchestration:/);
    expect(summary).toHaveClass("max-w-full");
    expect(summary.querySelector(".truncate")).not.toBeNull();
  });
});
