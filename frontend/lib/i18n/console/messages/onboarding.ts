import type { Messages } from "./common";

export const onboarding: Messages = {
  "onboarding.skip": { ru: "Пропустить", en: "Skip" },
  "onboarding.readDocs": { ru: "Читать в доке", en: "Read the docs" },
  "onboarding.gotIt": { ru: "Понятно", en: "Got it" },

  "onboarding.firstDeploy.title": {
    ru: "Первый деплой — в один клик",
    en: "Your first deploy is one click away",
  },
  "onboarding.firstDeploy.body": {
    ru: "Подключи репозиторий — соберём и выкатим его примерно за две минуты, дальше каждый push деплоится сам. Нет гита — загрузи архив. Домен и HTTPS выдаются сразу.",
    en: "Connect a repository — we build and ship it in about two minutes, and every later push deploys itself. No git? Upload an archive. Domain and HTTPS come out of the box.",
  },

  "onboarding.aiRouting.title": {
    ru: "GPT и Claude из России — двумя способами",
    en: "GPT and Claude from Russia — two ways",
  },
  "onboarding.aiRouting.body": {
    ru: "Есть свой ключ провайдера — маршрутизируем бесплатно. Ключа нет — возьмите наш, платите нам по факту расхода. Выбор меняется в любой момент.",
    en: "Have a provider key of your own? We route it for free. No key? Use ours and pay us for what you spend. The choice can be changed at any time.",
  },

  "onboarding.agent.title": { ru: "Познакомься с AI-агентом", en: "Meet the AI agent" },
  "onboarding.agent.body": {
    ru: "Чинит билды, деплоит, ставит переменные окружения. Просто опиши задачу словами.",
    en: "It fixes builds, deploys, and sets env variables. Just describe the task in words.",
  },
};
