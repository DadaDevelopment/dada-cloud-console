"use client";

import { useLang } from "@/lib/i18n/context";
import { AltLandingPage } from "@/components/marketing/alt-landing";

export default function DiscordBotHostingPage() {
  const { t } = useLang();
  return <AltLandingPage path="/hosting-discord-bot" g={t.discordBotAlt} utm="pseo_discord_bot" />;
}
