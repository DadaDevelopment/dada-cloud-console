"use client";
import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { projectsApi } from "@/lib/api";
import type { Project, Environment, Operation } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";

function timeAgo(dateStr: string): string {
  const date = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffSecs = Math.floor(diffMs / 1000);
  if (diffSecs < 60) return `${diffSecs}s ago`;
  const diffMins = Math.floor(diffSecs / 60);
  if (diffMins < 60) return `${diffMins}m ago`;
  const diffHours = Math.floor(diffMins / 60);
  if (diffHours < 24) return `${diffHours}h ago`;
  const diffDays = Math.floor(diffHours / 24);
  return `${diffDays}d ago`;
}

export default function ProjectOverviewPage() {
  const params = useParams<{ projectId: string }>();
  const projectId = params.projectId;

  const [project, setProject] = useState<Project | null>(null);
  const [environments, setEnvironments] = useState<Environment[]>([]);
  const [operations, setOperations] = useState<Operation[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    async function load() {
      try {
        const [detail, ops] = await Promise.all([
          projectsApi.get(projectId),
          projectsApi.operations(projectId),
        ]);
        setProject(detail.project);
        setEnvironments(detail.environments ?? []);
        setOperations((ops.operations ?? []).slice(0, 5));
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load project");
      } finally {
        setIsLoading(false);
      }
    }
    load();
  }, [projectId]);

  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Spinner size="lg" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
        {error}
      </div>
    );
  }

  if (!project) return null;

  return (
    <div>
      {/* Header */}
      <div className="mb-8 flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2 text-sm text-gray-500">
            <Link href="/projects" className="hover:text-gray-700">Projects</Link>
            <span>/</span>
            <span className="text-gray-900">{project.display_name}</span>
          </div>
          <h1 className="mt-2 text-2xl font-bold text-gray-900">{project.display_name}</h1>
          <p className="mt-0.5 font-mono text-sm text-gray-400">{project.name}</p>
        </div>
        <Link
          href={`/projects/${projectId}/databases`}
          className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 transition-colors"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 7v10c0 2.21 3.582 4 8 4s8-1.79 8-4V7M4 7c0 2.21 3.582 4 8 4s8-1.79 8-4M4 7c0-2.21 3.582-4 8-4s8 1.79 8 4" />
          </svg>
          Databases
        </Link>
      </div>

      {/* Environments */}
      {environments.length > 0 && (
        <div className="mb-8">
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500">Environments</h2>
          <div className="flex gap-3">
            {environments.map((env) => (
              <div
                key={env.id}
                className="flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-4 py-3 shadow-sm"
              >
                <span className={`h-2 w-2 rounded-full ${env.type === "prod" ? "bg-green-500" : "bg-blue-400"}`} />
                <span className="text-sm font-medium text-gray-700">{env.name}</span>
                <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                  env.type === "prod" ? "bg-green-100 text-green-700" : "bg-blue-100 text-blue-700"
                }`}>
                  {env.type}
                </span>
                <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                  env.runtime === "vm" ? "bg-amber-100 text-amber-700" : "bg-slate-100 text-slate-700"
                }`}>
                  {env.runtime === "vm" ? "VM" : "K8s"}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Quick actions */}
      <div className="mb-8 grid gap-4 sm:grid-cols-2 lg:grid-cols-5">
        <Link
          href={`/projects/${projectId}/databases`}
          className="group flex items-center gap-4 rounded-xl border border-gray-200 bg-white p-5 shadow-sm hover:border-blue-200 hover:shadow-md transition-all"
        >
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-blue-100 text-blue-600 group-hover:bg-blue-600 group-hover:text-white transition-colors">
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 7v10c0 2.21 3.582 4 8 4s8-1.79 8-4V7M4 7c0 2.21 3.582 4 8 4s8-1.79 8-4M4 7c0-2.21 3.582-4 8-4s8 1.79 8 4" />
            </svg>
          </div>
          <div>
            <p className="text-sm font-semibold text-gray-900">Databases</p>
            <p className="text-xs text-gray-400">Manage PostgreSQL databases</p>
          </div>
        </Link>

        <Link
          href={`/projects/${projectId}/apps`}
          className="group flex items-center gap-4 rounded-xl border border-gray-200 bg-white p-5 shadow-sm hover:border-blue-200 hover:shadow-md transition-all"
        >
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-green-100 text-green-600 group-hover:bg-green-600 group-hover:text-white transition-colors">
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 12h14M12 5l7 7-7 7" />
            </svg>
          </div>
          <div>
            <p className="text-sm font-semibold text-gray-900">Applications</p>
            <p className="text-xs text-gray-400">Manage app workloads</p>
          </div>
        </Link>

        <Link
          href={`/projects/${projectId}/models`}
          className="group flex items-center gap-4 rounded-xl border border-gray-200 bg-white p-5 shadow-sm hover:border-blue-200 hover:shadow-md transition-all"
        >
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-indigo-100 text-indigo-600 group-hover:bg-indigo-600 group-hover:text-white transition-colors">
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
            </svg>
          </div>
          <div>
            <p className="text-sm font-semibold text-gray-900">AI Models</p>
            <p className="text-xs text-gray-400">KServe inference services</p>
          </div>
        </Link>

        <Link
          href={`/projects/${projectId}/app-servers`}
          className="group flex items-center gap-4 rounded-xl border border-gray-200 bg-white p-5 shadow-sm hover:border-blue-200 hover:shadow-md transition-all"
        >
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-amber-100 text-amber-600 group-hover:bg-amber-600 group-hover:text-white transition-colors">
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 15.75V8.25A2.25 2.25 0 015.25 6h13.5A2.25 2.25 0 0121 8.25v7.5A2.25 2.25 0 0118.75 18H5.25A2.25 2.25 0 013 15.75zM7 9h10M7 12h4" />
            </svg>
          </div>
          <div>
            <p className="text-sm font-semibold text-gray-900">App Servers</p>
            <p className="text-xs text-gray-400">Provision and manage VM hosts</p>
          </div>
        </Link>

        <Link
          href={`/projects/${projectId}/operations`}
          className="group flex items-center gap-4 rounded-xl border border-gray-200 bg-white p-5 shadow-sm hover:border-blue-200 hover:shadow-md transition-all"
        >
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-purple-100 text-purple-600 group-hover:bg-purple-600 group-hover:text-white transition-colors">
            <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" />
            </svg>
          </div>
          <div>
            <p className="text-sm font-semibold text-gray-900">Operations</p>
            <p className="text-xs text-gray-400">View deployment history</p>
          </div>
        </Link>
      </div>

      {/* Recent operations */}
      <div>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-semibold uppercase tracking-wide text-gray-500">Recent Operations</h2>
          <Link
            href={`/projects/${projectId}/operations`}
            className="text-xs text-blue-600 hover:text-blue-700"
          >
            View all →
          </Link>
        </div>

        {operations.length === 0 ? (
          <div className="rounded-xl border border-dashed border-gray-200 py-10 text-center">
            <p className="text-sm text-gray-400">No operations yet</p>
          </div>
        ) : (
          <div className="overflow-hidden rounded-xl border border-gray-200 bg-white">
            {operations.map((op, idx) => (
              <div
                key={op.id}
                className={`flex items-center gap-4 px-5 py-4 ${
                  idx < operations.length - 1 ? "border-b border-gray-100" : ""
                }`}
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-gray-900">{op.action}</span>
                    <span className="text-xs text-gray-400">·</span>
                    <span className="font-mono text-xs text-gray-500">{op.resource_name}</span>
                  </div>
                  <div className="mt-0.5 text-xs text-gray-400">{timeAgo(op.created_at)}</div>
                </div>
                <Badge status={op.status} />
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
