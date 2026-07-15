"use client";

import { useLang } from "@/lib/i18n/context";
import { LegalDoc, privacyDoc } from "@/components/marketing/legal";

export default function PrivacyPage() {
  const { locale } = useLang();
  return <LegalDoc doc={privacyDoc[locale]} />;
}
