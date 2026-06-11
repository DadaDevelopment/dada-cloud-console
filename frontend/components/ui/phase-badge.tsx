// Shared resource-phase badge (previously duplicated per-page with slightly
// divergent tone logic). Multi-tone is a superset of the old two-tone usages.
function tone(phase: string): string {
  switch (phase.toLowerCase()) {
    case "ready":
      return "bg-green-100 text-green-700";
    case "failed":
      return "bg-red-100 text-red-700";
    case "waitingforapproval":
      return "bg-amber-100 text-amber-700";
    default:
      return "bg-yellow-100 text-yellow-700";
  }
}

export function PhaseBadge({ phase }: { phase?: string }) {
  const p = phase ?? "";
  return (
    <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${tone(p)}`}>
      {p || "Unknown"}
    </span>
  );
}
