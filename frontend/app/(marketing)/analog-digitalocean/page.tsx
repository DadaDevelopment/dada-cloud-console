"use client";

import { useLang } from "@/lib/i18n/context";
import { LandingGuide } from "@/components/marketing/landing-guide";

export default function DigitalOceanAlternativePage() {
  const { t } = useLang();
  return (
    <LandingGuide
      path="/analog-digitalocean"
      utm="utm_source=digitalocean-alt"
      copy={t.digitaloceanAlt}
      related={[
          { label: t.servers.heroTitle, href: "/cloud-servers" },
          { label: t.databases.heroTitle, href: "/databases" },
          { label: t.storage.heroTitle, href: "/storage" },
          { label: t.flyIoAlt.heroTitle, href: "/analog-fly-io" },
      ]}
    />
  );
}
