import * as React from "react";
import { clsx } from "clsx";
import type { OperationStatus } from "@/lib/types";

const statusColorMap: Record<string, string> = {
  Created: "bg-gray-100 dark:bg-gray-800 text-gray-800 dark:text-gray-200",
  Validated: "bg-blue-100 dark:bg-blue-950/40 text-blue-800 dark:text-blue-300",
  Queued: "bg-yellow-100 dark:bg-yellow-950/40 text-yellow-800 dark:text-yellow-300",
  Rendering: "bg-purple-100 dark:bg-purple-950/40 text-purple-800 dark:text-purple-300",
  CommittingToGit: "bg-indigo-100 dark:bg-indigo-950/40 text-indigo-800 dark:text-indigo-300",
  Committed: "bg-indigo-200 dark:bg-indigo-900/40 text-indigo-900 dark:text-indigo-200",
  WaitingForArgoSync: "bg-orange-100 dark:bg-orange-950/40 text-orange-800 dark:text-orange-300",
  Syncing: "bg-cyan-100 dark:bg-cyan-950/40 text-cyan-800 dark:text-cyan-300",
  Reconciling: "bg-teal-100 dark:bg-teal-950/40 text-teal-800 dark:text-teal-300",
  Ready: "bg-green-100 dark:bg-green-950/40 text-green-800 dark:text-green-300",
  Failed: "bg-red-100 dark:bg-red-950/40 text-red-800 dark:text-red-300",
  Cancelled: "bg-gray-200 dark:bg-gray-700 text-gray-600 dark:text-gray-400",
  WaitingForApproval: "bg-amber-100 dark:bg-amber-950/40 text-amber-800 dark:text-amber-300",
};

interface BadgeProps extends React.HTMLAttributes<HTMLSpanElement> {
  status?: OperationStatus | string;
}

export function Badge({ status, className, children, ...props }: BadgeProps) {
  const colorClass = status ? (statusColorMap[status] ?? "bg-gray-100 dark:bg-gray-800 text-gray-800 dark:text-gray-200") : "";

  return (
    <span
      className={clsx(
        "inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium",
        colorClass,
        className
      )}
      {...props}
    >
      {children ?? status}
    </span>
  );
}
