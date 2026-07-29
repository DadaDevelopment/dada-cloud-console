"use client";

import { useLang } from "@/lib/i18n/context";
import { AltLandingPage } from "@/components/marketing/alt-landing";

export default function FlaskHostingPage() {
  const { t } = useLang();
  return <AltLandingPage path="/hosting-flask" g={t.flaskAlt} utm="pseo_flask" />;
}
