"use client";

import { useLang } from "@/lib/i18n/context";
import { AltLandingPage } from "@/components/marketing/alt-landing";

export default function AiogramBotDeployPage() {
  const { t } = useLang();
  return <AltLandingPage path="/deploy-aiogram-bot" g={t.aiogramBotAlt} utm="pseo_aiogram" />;
}
