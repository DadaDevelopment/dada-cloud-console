"use client";

import { useLang } from "@/lib/i18n/context";
import { AltLandingPage } from "@/components/marketing/alt-landing";

export default function FastapiHostingPage() {
  const { t } = useLang();
  return <AltLandingPage path="/hosting-fastapi" g={t.fastapiAlt} utm="pseo_fastapi" />;
}
