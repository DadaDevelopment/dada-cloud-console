import type { Messages } from "./common";

export const passkey: Messages = {
  "passkey.title": { ru: "Создай passkey", en: "Create A Passkey" },
  "passkey.subtitle": {
    ru: "Защити аккаунт отпечатком пальца.",
    en: "Secure your account with a passkey.",
  },
  "passkey.body": {
    ru: "После создания вход будет без пароля — по отпечатку, FaceID или PIN устройства.",
    en: "Once created, you can sign in without a password next time.",
  },
  "passkey.note": {
    ru: "Устройство попросит отпечаток, Face ID или PIN — это и есть подтверждение.",
    en: "Your device will ask for a fingerprint, Face ID or PIN — that is the whole setup.",
  },
  "passkey.later": { ru: "Позже", en: "Maybe Later" },
  "passkey.create": { ru: "Создать", en: "Create Now" },
  "passkey.menuItem": { ru: "Passkey (вход по отпечатку)", en: "Passkey (fingerprint sign-in)" },
  "passkey.created": {
    ru: "Passkey создан. В следующий раз войдёшь без пароля.",
    en: "Passkey created. Next time you can sign in without a password.",
  },
};
