# Dada Cloud — 30-day acquisition plan (execution checklist)

Status: ready to execute. Blocked only on channel account credentials (see §4).
Context: activation path verified end-to-end (GitHub → build → live HTTPS URL). Constraint = zero repeatable inbound channel. ICP = non-DevOps RU founders / small teams / small agencies.

## 1. Top 3 channels (RU market, ranked)

1. **Product Radar + Habr "покажи свой проект"** — productradar.ru (RU Product Hunt, TG 14k+, top-3/week gets a post) + monthly Habr productradar threads + resident chat. Effort: low. Fit: exactly founders/pet-projects looking to launch fast without a team.
2. **Telegram indie/founder chats** — @ih_spb (Indie Hackers СПб), microfounders "Инди-хакеры", "Russian Hackers", "Твой пет проект"; catalog to expand: github.com/goq/telegram-list. Effort: medium (must live in the chat, answer, no spam). Fit: people without DevOps discussing deploy/hosting daily.
3. **VC.ru + Habr tutorial posts "чем заменить Vercel/Railway"** — live demand already ("Аналоги Vercel в России" articles pull traffic). Effort: high (one honest "deployed X in N min" tutorial). Fit: SEO long-tail "аналог Vercel Россия" catches the ICP at the moment of pain.

## 2. Ready-to-post messages (RU, honest, no hype)

**A. Build-in-public launch (Product Radar / TG):**
> Собрал Dada Cloud — деплой приложения из GitHub в прод с HTTPS-адресом без DevOps и без своего Kubernetes. Пушишь в репозиторий → сборка → живой URL. Сам устал настраивать CI и ingress руками, поэтому свернул это в один флоу. Работает из РФ без VPN, оплата в рублях. Сейчас ищу первые 10 команд, кто реально задеплоит свой проект и скажет, где больно. Ссылка: cloud.dada-tuda.ru. Готов помочь с первым деплоем лично.

**B. Answer in a "how to deploy X without DevOps" thread:**
> Если не хочешь возиться с VPS/nginx/сертификатами — вариантов из РФ немного, т.к. у Vercel/Railway проблемы с оплатой российской картой. Мы делаем Dada Cloud ровно под этот кейс: подключаешь GitHub-репо, получаешь HTTPS-URL, без VPN и с рублёвой оплатой. Не реклама-обещание — реально попробуй свой репозиторий и напиши, взлетело или нет: cloud.dada-tuda.ru. Если застрянешь на первом деплое — пиши в личку, помогу.

**C. Cold-DM to a small agency:**
> Привет! Вы делаете сайты/сервисы клиентам — как обычно деплоите и отдаёте прод? Мы сделали Dada Cloud: из GitHub в прод с HTTPS без настройки серверов, из РФ, оплата в рублях. Возможно, сэкономит вам DevOps-время на клиентских проектах. Дам бесплатный доступ на пару проектов в обмен на честный фидбэк — интересно попробовать?

## 3. Cheapest week-1 experiment

**Product Radar launch** (0 руб, ~2h). Success signal: **≥15 регистраций и ≥5 успешных первых деплоев** in 7 days from this channel.
Instrument: UTM `?utm_source=productradar` on every link (traffic attribution via Yandex.Metrika, already live). Funnel from DB + console metrics: registrations → time-to-first-deploy → first-deploy-success. Read conversion registration→successful-deploy and median TTFD.
Diagnosis rule: <5 deploys at ≥15 regs → activation problem, not channel. <15 regs → weak message/channel.

## 4. Access the operator must provide (the ONLY blocker)

- **Product Radar:** account on productradar.ru + join resident chat.
- **Telegram:** a warmed personal/brand account with history (new accounts get spam-banned) + posting rights where admin approval is needed. Monitoring: tgstat/telemetr free tier.
- **Habr + VC.ru:** accounts with karma (Habr throttles newcomers) — ASSUMPTION: may need a warmed or corporate blog.
- **Analytics:** already covered (Yandex.Metrika + console funnel metrics + dada_builds).
- **GitHub App:** nothing extra — activation flow already works.

## 5. Competitor angle vs Vercel/Railway (RU, true)

**"Работает из России: рублёвая оплата, без VPN, данные в РФ."** 152-ФЗ/242-ФЗ require storing RU citizens' personal data on servers in Russia — foreign hosting violates this. Vercel/Railway payment with a Russian card is unavailable (ASSUMPTION on specifics; corroborated by the wave of "аналог Vercel Россия" articles). Honest framing: not "we're faster", but "we're legal and payable here" — the one thing Vercel structurally cannot close on the RU market.

Sources: productradar.ru, Habr productradar, @ih_spb (tgstat), microfounders (telemetr), goq/telegram-list, Habr "Аналоги Vercel", Securiti 152-ФЗ.
