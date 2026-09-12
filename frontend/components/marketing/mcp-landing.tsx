"use client";

import { useState } from "react";
import Link from "next/link";
import { ArrowDown, ArrowRight, Bot, Cloud, Database, FileText, Layers } from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import { consoleHref, localeHref } from "@/lib/site";
import { GOAL_LANDING_CTA, reachGoal } from "@/lib/metrika";
import { CopyButton } from "@/components/ui/copy-button";
import { FaqJsonLd } from "./faq-jsonld";
import { HowToJsonLd } from "./howto-jsonld";
import styles from "./agent-products.module.css";

const UTM = "utm_source=mcp_landing";
const MCP_URL = "https://console.dada-tuda.ru/mcp";
const CLAUDE_COMMANDS = ["/plugin marketplace add DadaDevelopment/dada-cloud-console", "/plugin install dada-cloud@dada-cloud"];

export function McpLanding() {
  const { t, locale } = useLang();
  const [client, setClient] = useState<"claude" | "other">("claude");
  const isRu = locale === "ru";
  const copy = isRu ? {
    title: "Управляйте облаком через AI-агента",
    subtitle: "Подключите Claude, Cursor или другой MCP-клиент к DADA Cloud. Агент сможет развернуть приложение, проверить сборку и открыть логи — с правами вашего аккаунта.",
    primary: "Подключить MCP", account: "Войти в DADA Cloud", note: "Ваш агент и ваша модель. Приложения остаются в вашей консоли.",
    connectTitle: "Выберите свой клиент",
    connectDescription: "Для Claude Code — две команды. Для других клиентов — адрес сервера и публичный ID.",
    steps: [
      { title: "Добавьте подключение", text: "Установите плагин Claude Code или добавьте MCP-сервер в настройках своего клиента." },
      { title: "Войдите в DADA Cloud", text: "При первом обращении подтвердите вход в браузере. Клиент получит доступ с правами вашего аккаунта." },
      { title: "Поставьте задачу", text: "Попросите показать приложения, развернуть репозиторий или проверить логи." },
    ],
    install: ["1. Добавьте маркетплейс", "2. Установите плагин"],
    others: "Другие клиенты", otherHint: "Claude Desktop, Cursor и другие клиенты с подключением к удалённому MCP-серверу.",
    url: "Адрес MCP-сервера", oauth: "OAuth Client ID", oauthNote: "dada-mcp — публичный идентификатор, не секрет. Укажите его, если клиент запрашивает Client ID.",
    copy: "Скопировать", docs: "Подробная инструкция для клиентов", promptsTitle: "Начните с обычной задачи",
    prompts: ["Покажи приложения в моём проекте", "Открой логи последней сборки", "Разверни приложение из репозитория"],
    scope: "MCP соединяет агента с API облака. Доступ к проектам и разрешения на изменения проверяются так же, как в консоли.",
    faq: "Вопросы о подключении", closing: "Подключили? Попросите показать проекты.", closingNote: "Начните с просмотра, затем переходите к деплою и настройкам.",
  } : {
    title: "Run your cloud through your AI agent",
    subtitle: "Connect Claude, Cursor or another MCP client to DADA Cloud. Your agent can deploy an app, check a build and read logs with your account’s permissions.",
    primary: "Connect MCP", account: "Sign in to DADA Cloud", note: "Your agent and model. Your apps stay in your console.",
    connectTitle: "Choose your client",
    connectDescription: "Two commands for Claude Code. A server address and public ID for other clients.",
    steps: [
      { title: "Add the connection", text: "Install the Claude Code plugin or add the MCP server in your client’s settings." },
      { title: "Sign in to DADA Cloud", text: "Confirm browser sign-in on the first request. The client receives access with your account’s permissions." },
      { title: "Give it a task", text: "Ask to see your apps, deploy a repository or inspect logs." },
    ],
    install: ["1. Add the marketplace", "2. Install the plugin"],
    others: "Other clients", otherHint: "Claude Desktop, Cursor and other clients that connect to remote MCP servers.",
    url: "MCP server address", oauth: "OAuth Client ID", oauthNote: "dada-mcp is a public identifier, not a secret. Enter it if your client asks for a Client ID.",
    copy: "Copy", docs: "Detailed client setup guide", promptsTitle: "Start with an everyday task",
    prompts: ["Show the apps in my project", "Open the logs of the latest build", "Deploy an app from my repository"],
    scope: "MCP connects your agent to the cloud API. Project access and permission to make changes are checked just as they are in the console.",
    faq: "Connection questions", closing: "Connected? Ask to see your projects.", closingNote: "Start by looking around, then move on to deployment and configuration.",
  };
  const loginHref = consoleHref(`/login?${UTM}`);
  const trackAccount = (placement: string) => reachGoal(GOAL_LANDING_CTA, { source: "mcp_landing", placement });

  return (
    <div className={styles.page}>
      <FaqJsonLd path="/mcp" items={t.mcpAlt.faq} />
      <HowToJsonLd path="/mcp" name={copy.connectTitle} description={copy.connectDescription} steps={copy.steps.map((step) => ({ name: step.title, text: step.text }))} />
      <section className={styles.hero}>
        <div className={`${styles.wrap} ${styles.heroGrid}`}>
          <div>
            <p className={styles.eyebrow}>DADA CLOUD <span /> MCP</p>
            <h1 className={styles.title}>{copy.title}</h1>
            <p className={styles.lead}>{copy.subtitle}</p>
            <div className={styles.actions}>
              <a href="#connect" className={styles.primary}>{copy.primary}<ArrowRight size={17} aria-hidden="true" /></a>
              <Link href={loginHref} onClick={() => trackAccount("hero")} className={styles.secondary}>{copy.account}</Link>
            </div>
            <p className={styles.note}>{copy.note}</p>
          </div>
          <div className={styles.diagram} aria-label={isRu ? "MCP связывает вашего агента с ресурсами DADA Cloud" : "MCP connects your agent to DADA Cloud resources"}>
            <p className={styles.diagramLabel}>{isRu ? "От запроса к действию" : "From a request to an action"}</p>
            <div className={styles.agentNode}><Bot size={19} aria-hidden="true" />Claude · Cursor · MCP</div>
            <div className={styles.connector}><ArrowDown size={22} aria-hidden="true" /></div>
            <div className={styles.cloudNode}>
              <div className={styles.nodeHead}><span className={styles.nodeIcon}><Cloud size={26} aria-hidden="true" /></span><div><strong>DADA Cloud</strong><small>{isRu ? "Ваш аккаунт. Ваши права." : "Your account. Your permissions."}</small></div></div>
              <div className={styles.nodeResources}>
                <span><Layers size={13} aria-hidden="true" />{isRu ? "Приложения" : "Apps"}</span>
                <span><Database size={13} aria-hidden="true" />{isRu ? "Базы данных" : "Databases"}</span>
                <span><FileText size={13} aria-hidden="true" />{isRu ? "Логи" : "Logs"}</span>
              </div>
            </div>
            <p className={styles.diagramFoot}>{isRu ? "Те же ресурсы, что и в консоли" : "The same resources you see in the console"}</p>
          </div>
        </div>
      </section>

      <section id="connect" className={`${styles.section} ${styles.sectionWhite} scroll-mt-24`}>
        <div className={`${styles.wrap} ${styles.connectLayout}`}>
          <div><h2 className={styles.heading}>{copy.connectTitle}</h2><p className={`${styles.description} mt-4`}>{copy.connectDescription}</p><ol className={styles.connectSteps}>{copy.steps.map((step) => <li key={step.title}><div><strong className="text-slate-800">{step.title}</strong><br />{step.text}</div></li>)}</ol></div>
          <div className={styles.clientPanel}>
            <div className={styles.clientTabs} aria-label={copy.connectTitle}>
              <button type="button" className={`${styles.clientTab} ${client === "claude" ? styles.clientActive : ""}`} aria-pressed={client === "claude"} onClick={() => setClient("claude")}>Claude Code</button>
              <button type="button" className={`${styles.clientTab} ${client === "other" ? styles.clientActive : ""}`} aria-pressed={client === "other"} onClick={() => setClient("other")}>{copy.others}</button>
            </div>
            {client === "claude" ? CLAUDE_COMMANDS.map((command, i) => <Command key={command} label={copy.install[i]} value={command} copyLabel={copy.copy} />) : <>
              <p className={styles.description}>{copy.otherHint}</p>
              <Command label={copy.url} value={MCP_URL} copyLabel={copy.copy} />
              <Command label={copy.oauth} value="dada-mcp" copyLabel={copy.copy} />
              <p className={styles.note}>{copy.oauthNote}</p>
            </>}
            <Link href={localeHref("/developer/mcp-ai-agents", locale)} className="mt-6 inline-flex items-center gap-2 text-sm font-semibold text-blue-600 hover:underline">{copy.docs}<ArrowRight size={14} aria-hidden="true" /></Link>
          </div>
        </div>
      </section>

      <section className={styles.section}>
        <div className={styles.wrap}>
          <div className={styles.sectionHead}><h2 className={styles.heading}>{copy.promptsTitle}</h2><p className={styles.description}>{copy.scope}</p></div>
          <div className={styles.prompts}>{copy.prompts.map((prompt, i) => <blockquote key={prompt} className={styles.prompt}><span>0{i + 1}</span>«{prompt}»</blockquote>)}</div>
        </div>
      </section>
      <section className={`${styles.section} ${styles.sectionWhite}`}>
        <div className={styles.wrap}>
          <h2 className={`${styles.heading} mb-8`}>{copy.faq}</h2>
          {t.mcpAlt.faq.map((item) => <details key={item.q} className={styles.detail}><summary>{item.q}</summary><div className={styles.detailBody}><p>{item.a}</p></div></details>)}
        </div>
      </section>
      <div className={styles.wrap}><section className={styles.callout}><div><h2 className={styles.heading}>{copy.closing}</h2><p>{copy.closingNote}</p></div><Link href={loginHref} onClick={() => trackAccount("band")} className={styles.primary}>{copy.account}<ArrowRight size={17} aria-hidden="true" /></Link></section></div>
    </div>
  );
}

function Command({ label, value, copyLabel }: { label: string; value: string; copyLabel: string }) {
  return <div className={styles.commandBlock}><span className={styles.commandLabel}>{label}</span><div className={styles.commandRow}><code>{value}</code><CopyButton value={value} label={copyLabel} /></div></div>;
}
