"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Play, RotateCcw, Terminal } from "lucide-react";
import { clsx } from "clsx";
import type { DemoLine } from "@/lib/box-copy";
import { reportBoxEvent } from "@/lib/box-events";
import { useLang } from "@/lib/i18n/context";

/**
 * Scripted replay of the box lifecycle for the /box landing.
 *
 * This is deliberately a RECORDING, not a live shell, and it says so on screen.
 * The landing is a fake-door test (docs/product/box-product-brief.md) and the one
 * thing it must not do is imitate a box that doesn't exist yet — trust is the
 * product here.
 */

const LINE_DELAY_MS = 420;

export function BoxDemo({
  title,
  subtitle,
  recordingLabel,
  playLabel,
  replayLabel,
  lines,
}: {
  title: string;
  subtitle: string;
  recordingLabel: string;
  playLabel: string;
  replayLabel: string;
  lines: DemoLine[];
}) {
  const { locale } = useLang();
  const [shown, setShown] = useState(0);
  const [playing, setPlaying] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const clear = useCallback(() => {
    if (timer.current) {
      clearTimeout(timer.current);
      timer.current = null;
    }
  }, []);

  // "Are we still advancing" is DERIVED, not stored. An earlier version kept a
  // `running` flag and switched it off from inside the effect once the last line
  // showed — a synchronous setState in an effect body, which
  // react-hooks/set-state-in-effect rejects for causing cascading renders. It was
  // avoidable state: whether the replay is advancing is a function of two values
  // already on hand — did the user press play, and are there lines left.
  const advancing = playing && shown < lines.length;
  const finished = playing && shown >= lines.length;

  // Advance one line at a time. Cleared on unmount and on replay.
  useEffect(() => {
    if (!advancing) return;
    timer.current = setTimeout(() => setShown((n) => n + 1), LINE_DELAY_MS);
    return clear;
  }, [advancing, shown, clear]);

  useEffect(() => clear, [clear]);

  const start = () => {
    clear();
    setShown(0);
    setPlaying(true);
    // Locale was missing here, which made dada_box_funnel_events_total's locale
    // label default to "ru" for every English demo run.
    reportBoxEvent({ event: "demo_run", locale });
  };

  return (
    <section id="demo" className="scroll-mt-20 bg-slate-950 py-20">
      <div className="mx-auto max-w-5xl px-4 sm:px-6 lg:px-8">
        <div className="mb-10 max-w-3xl">
          <h2 className="text-3xl font-bold tracking-tight text-white">{title}</h2>
          <p className="mt-3 text-lg text-white/60">{subtitle}</p>
        </div>

        <div className="overflow-hidden rounded-xl border border-white/10 bg-black/60 shadow-2xl">
          <div className="flex items-center justify-between gap-4 border-b border-white/10 bg-white/5 px-4 py-2.5">
            <div className="flex items-center gap-2 text-xs font-medium text-white/50">
              <Terminal className="h-3.5 w-3.5" />
              <span className="uppercase tracking-wide">{recordingLabel}</span>
            </div>
            <button
              type="button"
              onClick={start}
              className="inline-flex items-center gap-1.5 rounded-md bg-blue-600 px-3 py-1.5 text-xs font-semibold text-white transition-colors hover:bg-blue-700"
            >
              {finished || advancing ? (
                <RotateCcw className="h-3.5 w-3.5" />
              ) : (
                <Play className="h-3.5 w-3.5" />
              )}
              {shown > 0 ? replayLabel : playLabel}
            </button>
          </div>

          <div className="min-h-[22rem] overflow-x-auto p-5 font-mono text-[13px] leading-relaxed">
            {shown === 0 && (
              <p className="text-white/30">{`# ${playLabel.toLowerCase()} →`}</p>
            )}
            {lines.slice(0, shown).map((line, i) => (
              <DemoRow key={`${i}-${line.text}`} line={line} />
            ))}
            {advancing && <span className="inline-block h-4 w-2 animate-pulse bg-white/60 align-middle" />}
          </div>
        </div>
      </div>
    </section>
  );
}

function DemoRow({ line }: { line: DemoLine }) {
  if (line.kind === "cmd") {
    return (
      <p className="whitespace-pre text-white">
        <span className="select-none text-blue-400">$ </span>
        {line.text}
      </p>
    );
  }
  return (
    <p
      className={clsx(
        "whitespace-pre",
        line.kind === "ok" && "text-emerald-400",
        line.kind === "out" && "text-white/50",
        line.kind === "note" && "italic text-blue-300/70",
      )}
    >
      {line.kind === "note" ? `— ${line.text}` : `  ${line.text}`}
    </p>
  );
}
