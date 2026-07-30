"use client";

import { useLang } from "@/lib/i18n/context";
import { LandingGuide } from "@/components/marketing/landing-guide";

export default function VibeCodingPage() {
  const { t } = useLang();
  return (
    <LandingGuide
      path="/deploy-vibe-coding"
      utm="utm_source=vibe-coding"
      copy={t.vibeCodingAlt}
      related={[
          { label: t.uploadDeployAlt.heroTitle, href: "/deploy-without-git" },
          { label: t.vercelAlt.heroTitle, href: "/analog-vercel" },
          { label: t.telegramBotAlt.heroTitle, href: "/hosting-telegram-bot" },
          { label: t.pricing.heroTitle, href: "/pricing" },
      ]}
    />
  );
}
