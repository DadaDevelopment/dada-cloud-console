import type { Messages } from "./common";

/**
 * The panel shown once a starter-template demo build succeeds: "the demo
 * worked, now ship your own code". Live psql showed most people who deploy a
 * starter template trigger one successful build and never come back -- there
 * was no explicit next step between "demo built" and "deploy your code", and
 * the platform's own next-step hint pointed at the starter repo the user has
 * no push access to. See components/deploy/starter-next-step.tsx.
 */
export const starterNext: Messages = {
  "starterNext.title": { ru: "Демо собралось. Теперь свой код", en: "The demo is live. Now ship your own code" },
  "starterNext.subtitle": {
    ru: "Это шаблон, который мы вам развернули для примера. Настоящий проект начинается с вашего кода — подключите репозиторий или загрузите архив.",
    en: "This is a sample template we deployed for you. Your real project starts with your own code -- connect a repo or upload an archive.",
  },
  "starterNext.connectGit.title": { ru: "Подключить свой репозиторий", en: "Connect your own repo" },
  "starterNext.connectGit.hint": {
    ru: "Свяжите GitHub-репозиторий и мы будем собирать и разворачивать его при каждом коммите.",
    en: "Link a GitHub repo and we will build and deploy it on every commit.",
  },
  "starterNext.connectGit.cta": { ru: "Подключить репозиторий", en: "Connect a repo" },
  "starterNext.upload.title": { ru: "Загрузить архив с кодом", en: "Upload your code as an archive" },
  "starterNext.upload.hint": {
    ru: "Нет GitHub-репозитория? Загрузите zip или tar.gz — мы определим фреймворк и порт сами.",
    en: "No GitHub repo yet? Upload a zip or tar.gz -- we detect the framework and port for you.",
  },
  "starterNext.upload.toggle.open": { ru: "Загрузить архив", en: "Upload an archive" },
  "starterNext.upload.toggle.close": { ru: "Свернуть", en: "Collapse" },
};
