"use client";

import { useLang } from "@/lib/i18n/context";
import { LandingGuide } from "@/components/marketing/landing-guide";

export default function HerokuAlternativePage() {
  const { t } = useLang();
  return (
    <LandingGuide
      path="/analog-heroku"
      utm="utm_source=heroku-alt"
      copy={t.herokuAlt}
      related={[
          { label: t.railwayAlt.heroTitle, href: "/analog-railway" },
          { label: t.renderAlt.heroTitle, href: "/analog-render" },
          { label: t.vercelAlt.heroTitle, href: "/analog-vercel" },
          { label: t.migrateVercel.heroTitle, href: "/migrate-vercel" },
      ]}
    />
  );
}
