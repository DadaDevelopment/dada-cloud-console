"use client";
import type { TooltipRenderProps } from "react-joyride";
import { useT } from "@/lib/i18n/console/context";

export interface OnboardingTooltipExtra {
  docsUrl: string;
  onDocs: () => void;
}

export function makeOnboardingTooltip({ docsUrl, onDocs }: OnboardingTooltipExtra) {
  return function OnboardingTooltip(props: TooltipRenderProps) {
    const { step, tooltipProps, primaryProps, skipProps } = props;
    const { t } = useT();
    return (
      <div
        {...tooltipProps}
        className="max-w-xs rounded-lg bg-white p-4 shadow-2xl dark:bg-gray-800 dark:text-gray-100"
      >
        {step.title ? <div className="mb-1 text-sm font-semibold">{step.title}</div> : null}
        <div className="mb-3 text-sm text-gray-600 dark:text-gray-300">{step.content}</div>
        <div className="flex items-center justify-between gap-3">
          <button
            {...skipProps}
            type="button"
            className="text-xs text-gray-500 hover:underline dark:text-gray-400"
          >
            {t("onboarding.skip")}
          </button>
          <div className="flex items-center gap-3">
            <a
              href={docsUrl}
              target="_blank"
              rel="noopener noreferrer"
              onClick={onDocs}
              className="text-xs text-blue-600 hover:underline dark:text-blue-400"
            >
              {t("onboarding.readDocs")}
            </a>
            <button
              {...primaryProps}
              type="button"
              className="rounded-md bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700"
            >
              {t("onboarding.gotIt")}
            </button>
          </div>
        </div>
      </div>
    );
  };
}
