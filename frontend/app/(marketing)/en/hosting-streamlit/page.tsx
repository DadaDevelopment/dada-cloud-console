"use client";

import { useLang } from "@/lib/i18n/context";
import { AltLandingPage } from "@/components/marketing/alt-landing";

export default function StreamlitHostingPage() {
  const { t } = useLang();
  return <AltLandingPage path="/hosting-streamlit" g={t.streamlitAlt} utm="pseo_streamlit" />;
}
