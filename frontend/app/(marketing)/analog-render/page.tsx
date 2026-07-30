"use client";

import { useLang } from "@/lib/i18n/context";
import { LandingGuide } from "@/components/marketing/landing-guide";

export default function RenderAlternativePage() {
  const { t } = useLang();
  return (
    <LandingGuide
      path="/analog-render"
      utm="utm_source=render-alt"
      copy={t.renderAlt}
      related={[
          { label: t.herokuAlt.heroTitle, href: "/analog-heroku" },
          { label: t.railwayAlt.heroTitle, href: "/analog-railway" },
          { label: t.vercelAlt.heroTitle, href: "/analog-vercel" },
          { label: t.flyIoAlt.heroTitle, href: "/analog-fly-io" },
      ]}
    />
  );
}
