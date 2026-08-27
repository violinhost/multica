import type { OrchestrationProjection } from "@multica/core/types";

export interface OrchestrationProjectionCardDetails {
  title: string;
  state: string;
  reason: string;
  nextAction: string;
}

/**
 * Keeps projection presentation separate from native issue fields. The
 * projection is optional, receipt-bound data; no projection means no card.
 */
export function orchestrationProjectionCardDetails(
  projection: OrchestrationProjection | undefined,
): OrchestrationProjectionCardDetails | null {
  if (!projection) return null;

  return {
    title: `${projection.stage} · ${projection.role}`,
    state: projection.substate,
    reason: projection.reason_code,
    nextAction: projection.next_action.code,
  };
}
