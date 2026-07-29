"use client";

import { useLang } from "@/lib/i18n/context";
import { AltLandingPage } from "@/components/marketing/alt-landing";

export default function VkBotHostingPage() {
  const { t } = useLang();
  return <AltLandingPage path="/hosting-vk-bot" g={t.vkBotAlt} utm="pseo_vk_bot" />;
}
