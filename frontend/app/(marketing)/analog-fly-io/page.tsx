"use client";

import { useLang } from "@/lib/i18n/context";
import { LandingGuide } from "@/components/marketing/landing-guide";

export default function FlyIoAlternativePage() {
  const { t } = useLang();
  return (
    <LandingGuide
      path="/analog-fly-io"
      utm="utm_source=fly-alt"
      copy={t.flyIoAlt}
      related={[
          { label: t.digitaloceanAlt.heroTitle, href: "/analog-digitalocean" },
          { label: t.renderAlt.heroTitle, href: "/analog-render" },
          { label: t.railwayAlt.heroTitle, href: "/analog-railway" },
          { label: t.servers.heroTitle, href: "/cloud-servers" },
      ]}
    />
  );
}
