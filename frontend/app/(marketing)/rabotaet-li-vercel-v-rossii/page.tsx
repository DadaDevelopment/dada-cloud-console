"use client";

import { useLang } from "@/lib/i18n/context";
import { LandingGuide } from "@/components/marketing/landing-guide";

export default function DoesVercelWorkInRussiaPage() {
  const { t } = useLang();

  return (
    <LandingGuide
      path="/rabotaet-li-vercel-v-rossii"
      utm="utm_source=vercel-in-russia"
      copy={t.vercelInRussia}
      related={[
        { label: t.payVercel.heroTitle, href: "/oplatit-vercel-iz-rossii" },
        { label: t.vercelAlt.heroTitle, href: "/analog-vercel" },
        { label: t.migrateVercel.heroTitle, href: "/migrate-vercel" },
        { label: t.statusRadar.heroTitle, href: "/status" },
      ]}
    />
  );
}
