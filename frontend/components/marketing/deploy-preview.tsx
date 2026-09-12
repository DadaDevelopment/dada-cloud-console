"use client";

import { useState } from "react";
import {
  ArrowUpRight,
  Check,
  Cloud,
  Database,
  GitBranch,
  Globe,
  Layers,
  Terminal,
} from "lucide-react";
import { useLang } from "@/lib/i18n/context";
import styles from "./deploy-preview.module.css";

/** An interactive illustration, deliberately labelled instead of posing as a live console. */
export function DeployPreview() {
  const { locale } = useLang();
  const en = locale === "en";
  const [view, setView] = useState(0);
  const labels = en
    ? ["Application", "Deploy", "Logs"]
    : ["Приложение", "Деплой", "Логи"];
  return (
    <figure className={styles.preview}>
      <div className={styles.window}>
        <div className={styles.topbar}>
          <span className={styles.brand}>
            <Cloud size={16} aria-hidden="true" /> DADA Cloud
          </span>
          <span>demo-project</span>
          <span className={styles.avatar} aria-hidden="true">
            D
          </span>
        </div>
        <div className={styles.body}>
          <div className={styles.rail} aria-hidden="true">
            <Layers size={17} />
            <Database size={17} />
            <Globe size={17} />
            <Terminal size={17} />
          </div>
          <div className={styles.content}>
            <div className={styles.breadcrumb}>
              {en ? "Applications" : "Приложения"}
              <span>/</span>example-api
            </div>
            <div className={styles.appHeading}>
              <h2>example-api</h2>
              <span className={styles.ready}>
                <Check size={12} aria-hidden="true" />
                {en ? "Ready" : "Готово"}
              </span>
            </div>
            <div
              className={styles.switcher}
              role="group"
              aria-label={
                en ? "Explore the illustration" : "Посмотреть схему интерфейса"
              }
            >
              {labels.map((label, index) => (
                <button
                  key={label}
                  type="button"
                  aria-pressed={view === index}
                  onClick={() => setView(index)}
                >
                  {label}
                </button>
              ))}
            </div>
            <div className={styles.panel} aria-live="polite">
              {view === 0 && (
                <>
                  <div className={styles.resource}>
                    <Globe size={17} aria-hidden="true" />
                    <div>
                      <span>HTTPS</span>
                      <strong>
                        example-api.dada-tuda.ru{" "}
                        <ArrowUpRight size={12} aria-hidden="true" />
                      </strong>
                    </div>
                  </div>
                  <div className={styles.resource}>
                    <GitBranch size={17} aria-hidden="true" />
                    <div>
                      <span>{en ? "Source" : "Исходный код"}</span>
                      <strong>
                        example / api <small>main</small>
                      </strong>
                    </div>
                  </div>
                  <div className={styles.resource}>
                    <Database size={17} aria-hidden="true" />
                    <div>
                      <span>{en ? "Database" : "База данных"}</span>
                      <strong>
                        PostgreSQL{" "}
                        <small>{en ? "connected" : "подключена"}</small>
                      </strong>
                    </div>
                  </div>
                </>
              )}
              {view === 1 && (
                <div className={styles.deploy}>
                  <code>$ git push origin main</code>
                  <ol>
                    {(en
                      ? [
                          "Build from source or Dockerfile",
                          "Launch the new version",
                          "Serve over HTTPS",
                        ]
                      : [
                          "Сборка из исходников или Dockerfile",
                          "Запуск новой версии",
                          "Приложение отвечает по HTTPS",
                        ]
                    ).map((label) => (
                      <li key={label}>
                        <Check size={15} aria-hidden="true" />
                        {label}
                      </li>
                    ))}
                  </ol>
                </div>
              )}
              {view === 2 && (
                <div className={styles.logs}>
                  <div>
                    <span />
                    {en
                      ? "Example application output"
                      : "Пример вывода приложения"}
                  </div>
                  <code>
                    <span>[app] Starting server…</span>
                    <span>[db] Connection established</span>
                    <span>[app] Listening on port 8080</span>
                    <span>[http] GET /health → 200 OK</span>
                  </code>
                </div>
              )}
            </div>
            <p className={styles.explanation}>
              {
                (en
                  ? [
                      "Code, database and public address in one project.",
                      "A push starts the next deployment. Previous versions remain available for rollback.",
                      "Read the application output alongside deployment history.",
                    ]
                  : [
                      "Код, база и публичный адрес — в одном проекте.",
                      "Push запускает новый деплой. Предыдущие версии доступны для отката.",
                      "Вывод приложения рядом с историей его запусков.",
                    ])[view]
              }
            </p>
          </div>
        </div>
      </div>
      <figcaption>
        <span>{en ? "INTERFACE ILLUSTRATION" : "СХЕМА ИНТЕРФЕЙСА"}</span>
        {en ? "Switch views to explore" : "Переключайте разделы"}
        <span aria-hidden="true">↖</span>
      </figcaption>
    </figure>
  );
}
