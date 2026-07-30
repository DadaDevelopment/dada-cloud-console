"use client";
import { useEffect, useRef, useState } from "react";
import { usePathname } from "next/navigation";
import { Joyride, STATUS, type Step, type EventData } from "react-joyride";
import { ONBOARDING_CAMPAIGNS } from "@/lib/onboarding/campaigns";
import { SELECT_WINDOW_MS, selectCampaignToFire } from "@/lib/onboarding/select";
import type { OnboardingCampaign, OnboardingStatus } from "@/lib/onboarding/types";
import { api } from "@/lib/api";
import { useT } from "@/lib/i18n/console/context";
import { makeOnboardingTooltip } from "./onboarding-tooltip";

const POLL_MS = 250;

function isDark(): boolean {
  return typeof document !== "undefined" && document.documentElement.classList.contains("dark");
}

export function OnboardingProvider({ suppressed }: { suppressed: boolean }) {
  const pathname = usePathname();
  const { t } = useT();
  const [statusMap, setStatusMap] = useState<Record<string, string> | null>(null);
  const [active, setActive] = useState<OnboardingCampaign | null>(null);
  const [run, setRun] = useState(false);
  const firedRef = useRef(false);

  useEffect(() => {
    let alive = true;
    api.onboarding
      .status()
      .then((m) => {
        if (alive) setStatusMap(m);
      })
      .catch(() => {
        if (alive) setStatusMap({});
      });
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    if (statusMap === null || suppressed || firedRef.current) return;
    const start = Date.now();
    const timer = setInterval(() => {
      const elapsed = Date.now() - start;
      if (firedRef.current || suppressed || elapsed > SELECT_WINDOW_MS) {
        clearInterval(timer);
        return;
      }
      const campaign = selectCampaignToFire(
        ONBOARDING_CAMPAIGNS,
        statusMap,
        { pathname, hasTarget: (sel) => !!document.querySelector(sel) },
        elapsed,
      );
      if (!campaign) return;
      clearInterval(timer);
      firedRef.current = true;
      setActive(campaign);
      setRun(true);
      report(campaign.key, "seen", 0);
    }, POLL_MS);
    return () => clearInterval(timer);
  }, [statusMap, suppressed, pathname]);

  function report(key: string, status: OnboardingStatus, step: number) {
    setStatusMap((prev) => ({ ...(prev ?? {}), [key]: status }));
    void api.onboarding.report(key, { status, step }).catch(() => {});
  }

  if (!active) return null;
  const campaign = active;

  const steps: Step[] = campaign.steps.map((s) => ({
    target: s.target,
    title: t(s.titleKey),
    content: t(s.bodyKey),
    skipBeacon: true,
    skipScroll: true,
    buttons: ["skip", "primary"],
    overlayColor: "rgba(0,0,0,0.55)",
    arrowColor: isDark() ? "#1f2937" : "#ffffff",
    zIndex: 60,
  }));

  function handleEvent(data: EventData) {
    const { status, index } = data;
    if (status === STATUS.SKIPPED) {
      report(campaign.key, "skipped", index);
      setRun(false);
      return;
    }
    if (status === STATUS.FINISHED) {
      report(campaign.key, "done", campaign.steps.length);
      setRun(false);
    }
  }

  return (
    <Joyride
      steps={steps}
      run={run}
      onEvent={handleEvent}
      tooltipComponent={makeOnboardingTooltip({
        docsUrl: campaign.docsUrl,
        onDocs: () => report(campaign.key, "seen", 0),
      })}
    />
  );
}
