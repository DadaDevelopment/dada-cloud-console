"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { ArrowRight, Check, Sparkles } from "lucide-react";

type ChatLine = { role: "user" | "assistant"; text: string };

type McpCopy = {
  tag: string;
  title: string;
  subtitle: string;
  chat: ChatLine[];
  bullets: string[];
  cta: string;
};

/**
 * Marketing section for the MCP / AI-agent feature: a static pitch on the left
 * and a self-animating chat mock on the right ("spin up a server" -> "done, sir").
 * The mock reveals lines one at a time, shows a typing indicator before each
 * assistant reply, then loops. Purely decorative — no network, no real agent.
 */
export function McpAgentSection({ copy, href }: { copy: McpCopy; href: string }) {
  return (
    <section className="bg-slate-950 py-20">
      <div className="mx-auto grid max-w-7xl items-center gap-12 px-4 sm:px-6 lg:grid-cols-2 lg:px-8">
        <div>
          <span className="mb-5 inline-flex items-center gap-2 rounded-full border border-blue-400/30 bg-blue-500/10 px-3 py-1 text-xs font-semibold text-blue-300">
            <Sparkles className="h-3.5 w-3.5" />
            {copy.tag}
          </span>
          <h2 className="max-w-xl text-3xl font-bold tracking-tight text-white sm:text-4xl">
            {copy.title}
          </h2>
          <p className="mt-4 max-w-xl text-lg text-white/70">{copy.subtitle}</p>
          <ul className="mt-7 space-y-3">
            {copy.bullets.map((b) => (
              <li key={b} className="flex items-start gap-3 text-sm text-white/80">
                <Check className="mt-0.5 h-4 w-4 shrink-0 text-blue-400" />
                <span>{b}</span>
              </li>
            ))}
          </ul>
          <Link
            href={href}
            className="mt-8 inline-flex items-center gap-2 rounded-md bg-blue-600 px-6 py-3 text-sm font-semibold text-white transition-colors hover:bg-blue-700"
          >
            {copy.cta}
            <ArrowRight className="h-4 w-4" />
          </Link>
        </div>

        <ChatMock lines={copy.chat} />
      </div>
    </section>
  );
}

function ChatMock({ lines }: { lines: ChatLine[] }) {
  const [shown, setShown] = useState(0);
  const [typing, setTyping] = useState(false);
  const timers = useRef<ReturnType<typeof setTimeout>[]>([]);

  useEffect(() => {
    const schedule = (fn: () => void, ms: number) => {
      timers.current.push(setTimeout(fn, ms));
    };

    const run = () => {
      let delay = 600;
      lines.forEach((line, i) => {
        if (line.role === "assistant") {
          schedule(() => setTyping(true), delay);
          delay += 900;
          schedule(() => {
            setTyping(false);
            setShown(i + 1);
          }, delay);
          delay += 700;
        } else {
          schedule(() => setShown(i + 1), delay);
          delay += 900;
        }
      });
      schedule(() => {
        setShown(0);
        setTyping(false);
      }, delay + 2600);
      schedule(run, delay + 3200);
    };

    run();
    return () => {
      timers.current.forEach(clearTimeout);
      timers.current = [];
    };
  }, [lines]);

  return (
    <div className="overflow-hidden rounded-2xl border border-white/10 bg-slate-900 shadow-2xl">
      <div className="flex items-center gap-2 border-b border-white/10 px-4 py-3">
        <span className="h-3 w-3 rounded-full bg-red-400/70" />
        <span className="h-3 w-3 rounded-full bg-yellow-400/70" />
        <span className="h-3 w-3 rounded-full bg-green-400/70" />
        <span className="ml-2 text-xs font-medium text-white/40">Claude · DADA Cloud MCP</span>
      </div>
      <div className="flex min-h-[300px] flex-col gap-3 p-5">
        {lines.slice(0, shown).map((line, i) => (
          <ChatBubble key={i} role={line.role} text={line.text} />
        ))}
        {typing && <TypingBubble />}
      </div>
    </div>
  );
}

function ChatBubble({ role, text }: ChatLine) {
  const isUser = role === "user";
  return (
    <div className={isUser ? "flex justify-end" : "flex justify-start"}>
      <div
        className={
          isUser
            ? "max-w-[80%] rounded-2xl rounded-br-sm bg-blue-600 px-4 py-2.5 text-sm text-white"
            : "max-w-[85%] rounded-2xl rounded-bl-sm bg-white/10 px-4 py-2.5 text-sm text-white/90"
        }
      >
        {text}
      </div>
    </div>
  );
}

function TypingBubble() {
  return (
    <div className="flex justify-start">
      <div className="flex items-center gap-1 rounded-2xl rounded-bl-sm bg-white/10 px-4 py-3">
        <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-white/60 [animation-delay:-0.3s]" />
        <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-white/60 [animation-delay:-0.15s]" />
        <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-white/60" />
      </div>
    </div>
  );
}
