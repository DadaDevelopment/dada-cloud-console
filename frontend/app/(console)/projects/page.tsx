"use client";
import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useProjectContext } from "@/lib/project-context";
import { Spinner } from "@/components/ui/spinner";

/**
 * The flat projects overview is gone: the top-bar switcher covers browsing and
 * creating projects. This route now just forwards into the console home — the
 * default/first project — while the provider bootstraps one when the user has
 * none. Lands the user inside a project instead of an empty list.
 */
export default function ProjectsPage() {
  const router = useRouter();
  const { defaultProjectId, projectsLoading } = useProjectContext();

  useEffect(() => {
    if (!projectsLoading && defaultProjectId) {
      router.replace(`/projects/${defaultProjectId}`);
    }
  }, [projectsLoading, defaultProjectId, router]);

  return (
    <div className="flex h-[60vh] items-center justify-center">
      <Spinner size="lg" />
    </div>
  );
}
