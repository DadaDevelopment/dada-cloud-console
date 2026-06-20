import type { BuildStatus } from "@/lib/types";

// Maps a build lifecycle status to a tone + human label. The build state
// machine is queued→detecting→building→pushing→success/failed (+canceled).
const tone: Record<BuildStatus, string> = {
  queued: "bg-gray-100 text-gray-700",
  detecting: "bg-blue-100 text-blue-700",
  building: "bg-indigo-100 text-indigo-700",
  pushing: "bg-purple-100 text-purple-700",
  success: "bg-green-100 text-green-700",
  failed: "bg-red-100 text-red-700",
  canceled: "bg-gray-200 text-gray-600",
};

const label: Record<BuildStatus, string> = {
  queued: "Queued",
  detecting: "Detecting",
  building: "Building",
  pushing: "Pushing",
  success: "Success",
  failed: "Failed",
  canceled: "Canceled",
};

export const ACTIVE_BUILD_STATES: BuildStatus[] = ["queued", "detecting", "building", "pushing"];

export function isBuildActive(status: BuildStatus): boolean {
  return ACTIVE_BUILD_STATES.includes(status);
}

export function BuildStatusBadge({ status }: { status: BuildStatus }) {
  return (
    <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${tone[status]}`}>
      {label[status]}
    </span>
  );
}
