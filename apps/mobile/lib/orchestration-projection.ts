import type { OrchestrationProjection } from "@multica/core/types";

export interface OrchestrationProjectionCardDetails {
  title: string;
  state: string;
  reason: string;
  elapsed: string;
  sla: string;
  nextAction: string;
}

// Presentation reads only the optional receipt-bound projection. It never
// derives orchestration state from title, native status, comments, or runs.
export function orchestrationProjectionCardDetails(
  projection: OrchestrationProjection | undefined,
): OrchestrationProjectionCardDetails | null {
  if (!projection) return null;
  return {
    title: `${projection.stage} · ${projection.role}`,
    state: projection.substate,
    reason: projection.reason_code,
    elapsed: `${projection.elapsed_seconds}s`,
    sla: projection.sla_posture,
    nextAction: projection.next_action.target
      ? `${projection.next_action.code} → ${projection.next_action.target}`
      : projection.next_action.code,
  };
}
