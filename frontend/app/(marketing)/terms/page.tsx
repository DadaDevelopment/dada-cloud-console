"use client";

import { useLang } from "@/lib/i18n/context";
import { LegalDoc, termsDoc } from "@/components/marketing/legal";

export default function TermsPage() {
  const { locale } = useLang();
  return <LegalDoc doc={termsDoc[locale]} />;
}
