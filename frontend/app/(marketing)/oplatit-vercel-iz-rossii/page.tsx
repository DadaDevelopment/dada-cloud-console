"use client";

import { useLang } from "@/lib/i18n/context";
import { LandingGuide } from "@/components/marketing/landing-guide";

export default function PayVercelFromRussiaPage() {
  const { t } = useLang();

  return (
    <LandingGuide
      path="/oplatit-vercel-iz-rossii"
      utm="utm_source=pay-vercel"
      copy={t.payVercel}
      related={[
        { label: t.vercelInRussia.heroTitle, href: "/rabotaet-li-vercel-v-rossii" },
        { label: t.vercelAlt.heroTitle, href: "/analog-vercel" },
        { label: t.migrateVercel.heroTitle, href: "/migrate-vercel" },
        { label: t.statusRadar.heroTitle, href: "/status" },
      ]}
    />
  );
}
