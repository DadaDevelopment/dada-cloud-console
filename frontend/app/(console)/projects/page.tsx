"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { projectsApi } from "@/lib/api";
import type { MemberRole, Project } from "@/lib/types";
import { roleColors, roleLabels } from "@/lib/rbac";

function SkeletonCard() {
  return (
    <div className="animate-pulse rounded-xl border border-gray-200 bg-white p-6">
      <div className="mb-3 h-5 w-3/4 rounded bg-gray-200" />
      <div className="mb-4 h-3 w-1/2 rounded bg-gray-100" />
      <div className="flex items-center justify-between">
        <div className="h-5 w-16 rounded-full bg-gray-200" />
        <div className="h-8 w-20 rounded-lg bg-gray-200" />
      </div>
    </div>
  );
}

export default function ProjectsPage() {
  const [projects, setProjects] = useState<Project[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    projectsApi
      .list()
      .then((data) => setProjects(data.projects ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load projects"))
      .finally(() => setIsLoading(false));
  }, []);

  return (
    <div>
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-gray-900">Projects</h1>
        <p className="mt-1 text-sm text-gray-500">Your cloud infrastructure projects</p>
      </div>

      {error && (
        <div className="mb-6 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}

      {isLoading ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <SkeletonCard />
          <SkeletonCard />
          <SkeletonCard />
        </div>
      ) : projects.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-gray-50 py-16">
          <svg className="mb-3 h-12 w-12 text-gray-300" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M6 6.878V6a2.25 2.25 0 012.25-2.25h7.5A2.25 2.25 0 0118 6v.878m-12 0c.235-.083.487-.128.75-.128h10.5c.263 0 .515.045.75.128m-12 0A2.25 2.25 0 004.5 9v.878m13.5-3A2.25 2.25 0 0119.5 9v.878m0 0a2.246 2.246 0 00-.75-.128H5.25c-.263 0-.515.045-.75.128m15 0A2.25 2.25 0 0121 12v6a2.25 2.25 0 01-2.25 2.25H5.25A2.25 2.25 0 013 18v-6c0-.98.626-1.813 1.5-2.122" />
          </svg>
          <p className="text-sm font-medium text-gray-500">No projects found</p>
          <p className="mt-1 text-xs text-gray-400">Contact your administrator to get access to a project.</p>
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {projects.map((project) => {
            const roleKey = project.role as MemberRole | undefined;
            const roleLabel = roleKey ? roleLabels[roleKey] ?? roleKey : "";
            const roleColor = roleKey ? roleColors[roleKey] ?? "bg-gray-100 text-gray-600" : "bg-gray-100 text-gray-600";
            return (
              <div
                key={project.id}
                className="group rounded-xl border border-gray-200 bg-white p-6 shadow-sm transition-shadow hover:shadow-md"
              >
                <div className="mb-1 text-base font-semibold text-gray-900">
                  {project.display_name}
                </div>
                <div className="mb-4 font-mono text-xs text-gray-400">{project.name}</div>
                <div className="flex items-center justify-between">
                  {roleKey ? (
                    <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${roleColor}`}>
                      {roleLabel}
                    </span>
                  ) : (
                    <span />
                  )}
                  <Link
                    href={`/projects/${project.id}`}
                    className="inline-flex items-center gap-1 rounded-lg bg-blue-50 px-3 py-1.5 text-xs font-medium text-blue-600 hover:bg-blue-100 transition-colors"
                  >
                    View
                    <svg className="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
                    </svg>
                  </Link>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
