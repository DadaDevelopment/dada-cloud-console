"use client";

import { useLang } from "@/lib/i18n/context";
import { AltLandingPage } from "@/components/marketing/alt-landing";

export default function TelegramBotHostingPage() {
  const { t } = useLang();
  return <AltLandingPage path="/hosting-telegram-bot" g={t.telegramBotAlt} utm="door_a" />;
}
