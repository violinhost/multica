import type { OrchestrationProjection } from "@multica/core/types";

/**
 * Displays only the receipt-bound orchestration payload. Native issue status is
 * intentionally rendered by the surrounding status component and is never
 * inferred or replaced here.
 */
export function OrchestrationProjectionSummary({
  projection,
  compact = false,
}: {
  projection: OrchestrationProjection | undefined;
  compact?: boolean;
}) {
  if (!projection) return null;

  const elapsed = `${projection.elapsed_seconds}s`;
  const owner = projection.next_action.target
    ? ` → ${projection.next_action.target}`
    : "";

  if (compact) {
    return (
      <span
        className="inline-flex max-w-full items-center gap-1 rounded bg-muted/60 px-1.5 py-0.5 text-micro text-muted-foreground"
        aria-label={`Orchestration: ${projection.stage}, ${projection.role}, ${projection.substate}; ${projection.reason_code}; ${projection.sla_posture}; next ${projection.next_action.code}${owner}`}
      >
        <span className="truncate">{projection.stage} · {projection.role} · {projection.substate}</span>
      </span>
    );
  }

  return (
    <section
      aria-label="Orchestration projection"
      className="rounded-md border border-border bg-muted/30 px-3 py-2 text-caption text-muted-foreground"
    >
      <p className="font-medium text-foreground">Orchestration · {projection.stage} · {projection.role}</p>
      <p>{projection.substate} · {projection.reason_code}</p>
      <p>Elapsed {elapsed} · SLA {projection.sla_posture}</p>
      <p>Next: {projection.next_action.code}{owner}</p>
    </section>
  );
}
