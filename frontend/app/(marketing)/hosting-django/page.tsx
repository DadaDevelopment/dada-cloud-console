"use client";

import { useLang } from "@/lib/i18n/context";
import { AltLandingPage } from "@/components/marketing/alt-landing";

export default function DjangoHostingPage() {
  const { t } = useLang();
  return <AltLandingPage path="/hosting-django" g={t.djangoAlt} utm="pseo_django" />;
}
