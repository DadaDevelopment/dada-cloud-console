function tone(phase: string): string {
  switch (phase.toLowerCase()) {
    case "ready":
      return "bg-green-100 dark:bg-green-950/40 text-green-700 dark:text-green-300";
    case "failed":
      return "bg-red-100 dark:bg-red-950/40 text-red-700 dark:text-red-300";
    case "waitingforapproval":
      return "bg-amber-100 dark:bg-amber-950/40 text-amber-700 dark:text-amber-300";
    default:
      return "bg-yellow-100 dark:bg-yellow-950/40 text-yellow-700 dark:text-yellow-300";
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
