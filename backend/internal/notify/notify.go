// Package notify sends best-effort operator email over the shared Postbox
// SMTP relay (same credentials Keycloak uses for its own mail). Every send is
// fire-and-forget: callers launch it off the request's hot path and log
// errors instead of propagating them, so a mail outage never blocks a user
// action.
package notify

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"mime/quotedprintable"
	"net/smtp"
	"regexp"
	"strings"
	"time"
)

// Notifier holds the SMTP endpoint used for operator mail.
type Notifier struct {
	host string
	port int
	user string
	pass string
	from string
}

// New builds a Notifier. It returns nil when host or from is empty, so a
// misconfigured deployment degrades to no-op instead of erroring on every send.
func New(host string, port int, user, pass, from string) *Notifier {
	if host == "" || from == "" {
		return nil
	}
	return &Notifier{host: host, port: port, user: user, pass: pass, from: from}
}

// ComposeSignup builds the subject and plaintext body for a new-user-signup
// notification. totalUsers is -1 when the caller could not cheaply compute it
// (the line is then omitted rather than shown as a wrong number).
func ComposeSignup(email, username, createdAtUTC string, totalUsers int) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: новая регистрация %s", email)
	var b strings.Builder
	fmt.Fprintf(&b, "Новый пользователь зарегистрировался в Dada Cloud.\n\n")
	fmt.Fprintf(&b, "Email: %s\n", email)
	fmt.Fprintf(&b, "Username: %s\n", username)
	fmt.Fprintf(&b, "Создан: %s (UTC)\n", createdAtUTC)
	if totalUsers >= 0 {
		fmt.Fprintf(&b, "Всего пользователей: %d\n", totalUsers)
	}
	return subject, b.String()
}

// ComposeAudit builds the subject and plaintext body for a significant-action
// owner notification: one email per curated audit_events row (app/project/db
// create, git connect, build trigger, domain attach, app delete).
func ComposeAudit(action, actorEmail, resourceName, projectName, createdAtUTC string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: %s — %s", action, actorEmail)
	var b strings.Builder
	fmt.Fprintf(&b, "Значимое событие в Dada Cloud.\n\n")
	fmt.Fprintf(&b, "Пользователь: %s\n", actorEmail)
	fmt.Fprintf(&b, "Действие: %s\n", action)
	fmt.Fprintf(&b, "Ресурс: %s\n", resourceName)
	fmt.Fprintf(&b, "Проект: %s\n", projectName)
	fmt.Fprintf(&b, "Время: %s (UTC)\n", createdAtUTC)
	return subject, b.String()
}

// ComposePaymentSuccess builds the subject and plaintext body for the
// customer-facing payment-success email (YooKassa checkout).
//
// autopayArmed says the customer also agreed to automatic renewal. Saying so
// here, in writing, on the day they agreed — together with how to turn it off
// — is what separates a subscription from a surprise.
func ComposePaymentSuccess(planName, amountValue string, autopayArmed bool) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: оплата тарифа %s прошла успешно", planName)
	var b strings.Builder
	fmt.Fprintf(&b, "Спасибо! Платёж на тариф %s (%s ₽) успешно проведён.\n\n", planName, amountValue)
	b.WriteString("Новый тариф уже активен в консоли.\n")
	if autopayArmed {
		b.WriteString("\nВы включили автопродление: за день до окончания месяца мы спишем ")
		fmt.Fprintf(&b, "%s ₽ с той же карты и продлим тариф ещё на 30 дней.\n", amountValue)
		b.WriteString("Отключить автопродление можно в любой момент: консоль → проект → Billing.\n")
	}
	return subject, b.String()
}

// ComposeAutopayCharged builds the customer-facing notice that automatic
// renewal took money without them being present. It is sent on every
// successful auto-charge, never batched and never suppressed: a silent
// recurring debit is the single fastest way to earn a chargeback.
func ComposeAutopayCharged(planName, amountValue, expiresAtUTC string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: тариф %s продлён автоматически", planName)
	var b strings.Builder
	fmt.Fprintf(&b, "Списали %s ₽ по автопродлению тарифа %s.\n\n", amountValue, planName)
	fmt.Fprintf(&b, "Тариф активен до %s (UTC).\n", expiresAtUTC)
	b.WriteString("Чек отправлен отдельным письмом от ЮKassa.\n")
	b.WriteString("Отключить автопродление: консоль → проект → Billing.\n")
	return subject, b.String()
}

// ComposeAutopayFailed builds the customer-facing notice that an automatic
// renewal was declined. attempt/maxAttempts are stated plainly so the
// customer knows whether anything will be retried, and final says the account
// is now on the manual path — the difference decides whether they need to act
// today.
func ComposeAutopayFailed(planName, amountValue, reason, expiresAtUTC string, attempt, maxAttempts int, final bool) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: не удалось продлить тариф %s", planName)
	var b strings.Builder
	fmt.Fprintf(&b, "Автосписание %s ₽ за тариф %s не прошло (попытка %d из %d).\n", amountValue, planName, attempt, maxAttempts)
	if reason != "" {
		fmt.Fprintf(&b, "Причина: %s\n", reason)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Тариф действует до %s (UTC), после этого ещё 3 дня работает как есть.\n", expiresAtUTC)
	if final {
		b.WriteString("Больше автоматических попыток не будет — автопродление отключено.\n")
		b.WriteString("Продлите вручную: консоль → проект → Billing → «Оплатить».\n")
	} else {
		b.WriteString("Мы повторим попытку автоматически. Если карта больше не действует, оплатите вручную:\n")
		b.WriteString("консоль → проект → Billing → «Оплатить».\n")
	}
	b.WriteString("Работающие приложения не останавливаются.\n")
	return subject, b.String()
}

// ComposePlanExpiryReminder builds the customer-facing reminder that a paid
// plan's 30-day term is running out (plan-expiry sweeper).
func ComposePlanExpiryReminder(planName, expiresAtUTC string, daysLeft int) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: тариф %s истекает через %d дн.", planName, daysLeft)
	var b strings.Builder
	fmt.Fprintf(&b, "Срок действия тарифа %s заканчивается %s (UTC).\n\n", planName, expiresAtUTC)
	b.WriteString("Продлить можно в консоли: откройте проект → Billing → «Оплатить».\n")
	b.WriteString("Если тариф не продлить, через 3 дня после окончания аккаунт перейдёт на Free.\n")
	b.WriteString("Работающие приложения не останавливаются — лимиты Free применяются только к новым ресурсам.\n")
	return subject, b.String()
}

// ComposePlanDowngraded builds the customer-facing notice that an expired paid
// plan lapsed to Free after the grace period (plan-expiry sweeper).
func ComposePlanDowngraded(planName string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: тариф %s истёк — аккаунт переведён на Free", planName)
	var b strings.Builder
	fmt.Fprintf(&b, "Срок действия тарифа %s закончился, и аккаунт переведён на Free.\n\n", planName)
	b.WriteString("Работающие приложения не тронуты — лимиты Free действуют только на создание новых ресурсов.\n")
	b.WriteString("Вернуть тариф можно в любой момент: консоль → проект → Billing → «Оплатить».\n")
	return subject, b.String()
}

// QuotaLine is one resource the grandfathering notice names: how much the org
// has now against what the free tier includes.
type QuotaLine struct {
	Label string
	Used  int
	Limit int
}

// ComposeQuotaGraceReminder builds the customer-facing notice that the
// grandfathering window is closing.
//
// It leads with what does NOT change, because that is the honest headline:
// these users were promised a free tier and nothing they built is being taken
// away. The numbers are the org's own, so the message is actionable rather
// than a generic policy announcement; when nothing is over the limit the list
// is empty and the mail says exactly that.
func ComposeQuotaGraceReminder(graceUntilUTC string, daysLeft int, over []QuotaLine) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: бесплатные лимиты начнут действовать через %d дн.", daysLeft)
	var b strings.Builder
	fmt.Fprintf(&b, "С %s (UTC) на аккаунте начинают действовать лимиты бесплатного тарифа.\n\n", graceUntilUTC)
	b.WriteString("Всё, что уже создано, продолжает работать — ничего не останавливается и не удаляется.\n")
	b.WriteString("Лимиты применяются только к созданию новых ресурсов.\n\n")
	if len(over) == 0 {
		b.WriteString("Вы сейчас внутри бесплатных лимитов — делать ничего не нужно.\n")
	} else {
		b.WriteString("Сейчас сверх бесплатного тарифа:\n")
		for _, l := range over {
			fmt.Fprintf(&b, "  - %s: %d, бесплатно %d\n", l.Label, l.Used, l.Limit)
		}
		b.WriteString("\nЧтобы создавать новые ресурсы после этой даты, нужен платный тариф:\n")
		b.WriteString("консоль → проект → Billing → «Оплатить».\n")
	}
	return subject, b.String()
}

// ComposeReactivation builds the letter sent to an account that signed up and
// never shipped anything. Every one of those accounts already has a project,
// so the blocker is not "how do I start" — it is "what do I put in it", and
// the letter is written against that: one link, one offer, no tour.
//
// promoLink is the per-recipient tracked URL; it is the ONLY link in the body,
// because a second link splits the click and makes the campaign unmeasurable.
// planName and days describe the grant waiting behind it.
func ComposeReactivation(planName string, days int, promoLink string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: %s на %d дней — и готовые шаблоны, чтобы не начинать с пустого экрана", planName, days)
	var b strings.Builder
	b.WriteString("Вы завели проект в Dada Cloud, но так и не выкатили приложение.\n\n")
	b.WriteString("Чаще всего дело не в платформе, а в том, что непонятно, с чего начать. Поэтому мы приготовили две вещи.\n\n")
	fmt.Fprintf(&b, "Первое — тариф %s на %d дней бесплатно. Карта не нужна, по окончании срока аккаунт сам вернётся на Free, ничего не спишется.\n", planName, days)
	b.WriteString("Второе — каталог готовых шаблонов: выбираете репозиторий, жмёте «Задеплоить», через пару минут приложение живёт на своём домене с HTTPS.\n\n")
	fmt.Fprintf(&b, "Забрать тариф и посмотреть шаблоны: %s\n\n", promoLink)
	b.WriteString("Если что-то не поедет — ответьте на это письмо, разберёмся вместе.\n")
	return subject, b.String()
}

// ComposeReactivationHTML builds the HTML half of the reactivation letter.
//
// It says exactly what the text half says — the two parts of a
// multipart/alternative message are the same letter, and a client picking one
// over the other must not change the offer.
//
// heroURL is a wide banner carrying the offer as a picture. The first wave
// went out as a wall of text and nobody read past the first line; the banner
// exists so the offer survives a two-second glance. It is decorative on
// purpose: its alt text repeats the offer, and every word of it is also in the
// body, so a client with remote images off loses nothing but the picture.
//
// pixelURL is a 1x1 image whose fetch is the only open signal there is. It
// sits at the end of the body: an image ahead of the content delays the
// visible part of the letter on a slow connection for no measurement gain.
// Nothing in the letter depends on it loading, and a client with remote images
// off still gets the whole message plus the link.
func ComposeReactivationHTML(planName string, days int, promoLink, pixelURL, heroURL string) string {
	var b strings.Builder
	b.WriteString(`<div style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;font-size:15px;line-height:1.55;color:#111827;max-width:600px">`)
	writeMailHero(&b, heroURL, promoLink, fmt.Sprintf("%s на %d дней бесплатно", planName, days))
	b.WriteString(`<p>Вы завели проект в Dada Cloud, но так и не выкатили приложение.</p>`)
	b.WriteString(`<p>Чаще всего дело не в платформе, а в том, что непонятно, с чего начать. Поэтому мы приготовили две вещи.</p>`)
	fmt.Fprintf(&b, `<p><b>Первое</b> — тариф %s на %d дней бесплатно. Карта не нужна, по окончании срока аккаунт сам вернётся на Free, ничего не спишется.</p>`,
		html.EscapeString(planName), days)
	b.WriteString(`<p><b>Второе</b> — каталог готовых шаблонов: выбираете репозиторий, жмёте «Задеплоить», через пару минут приложение живёт на своём домене с HTTPS.</p>`)
	fmt.Fprintf(&b, `<p><a href="%s" style="display:inline-block;background:#2563eb;color:#ffffff;text-decoration:none;padding:12px 22px;border-radius:8px;font-weight:600">Забрать тариф и посмотреть шаблоны</a></p>`,
		html.EscapeString(promoLink))
	fmt.Fprintf(&b, `<p style="font-size:13px;color:#6b7280">Если кнопка не нажимается, откройте ссылку: <a href="%s">%s</a></p>`,
		html.EscapeString(promoLink), html.EscapeString(promoLink))
	b.WriteString(`<p>Если что-то не поедет — ответьте на это письмо, разберёмся вместе.</p>`)
	if pixelURL != "" {
		fmt.Fprintf(&b, `<img src="%s" width="1" height="1" alt="" style="display:block;width:1px;height:1px;border:0">`,
			html.EscapeString(pixelURL))
	}
	b.WriteString(`</div>`)
	return b.String()
}

// writeMailHero writes the banner that opens a campaign letter: the picture,
// linked to the same tracked URL as the button, with alt text that carries the
// headline when the picture does not load.
//
// The width is fixed at 600 because that is what mail clients give a message
// body; the image itself is rendered at 1200 so it is not mush on a retina
// screen. An empty heroURL writes nothing at all, which is what tests and any
// deployment without a public asset host want.
func writeMailHero(b *strings.Builder, heroURL, linkURL, alt string) {
	if heroURL == "" {
		return
	}
	fmt.Fprintf(b, `<p style="margin:0 0 20px"><a href="%s"><img src="%s" alt="%s" width="600" style="display:block;width:100%%;max-width:600px;height:auto;border:0;border-radius:12px"></a></p>`,
		html.EscapeString(linkURL), html.EscapeString(heroURL), html.EscapeString(alt))
}

// ComposeReactivationFix builds the second-wave letter: the recipient took the
// promo, reached the deploy screen and stalled there. The first wave's data
// showed the stall was ours -- the git-import page could not connect anything
// but GitHub -- so this letter does not repeat the offer, it announces the
// fix. The tone is an apology that carries news, not a nudge.
//
// promoLink stays the ONLY link for the same reason as in the first wave: a
// second link splits the click and makes the wave unmeasurable.
func ComposeReactivationFix(planName, expires string, promoLink string) (subject, body string) {
	subject = "Dada Cloud: деплой по ссылке на репозиторий теперь работает"
	var b strings.Builder
	b.WriteString("Вы активировали тариф в Dada Cloud, но приложение так и не выехало.\n\n")
	b.WriteString("Скорее всего, споткнулись об нас: подключить репозиторий не с GitHub было нельзя, кнопка была, а за ней ничего. Это уже починено.\n\n")
	b.WriteString("Теперь достаточно вставить ссылку на репозиторий — GitHub, GitLab, любой публичный Git. Токен не нужен, приватный тоже можно, с токеном.\n\n")
	if expires != "" {
		fmt.Fprintf(&b, "Тариф %s ещё действует до %s, ничего заново активировать не надо.\n\n", planName, expires)
	} else {
		fmt.Fprintf(&b, "Тариф %s ещё действует, ничего заново активировать не надо.\n\n", planName)
	}
	fmt.Fprintf(&b, "Задеплоить по ссылке: %s\n\n", promoLink)
	b.WriteString("Если снова что-то упрётся — просто ответьте на это письмо.\n")
	return subject, b.String()
}

// ComposeReactivationFixHTML is the HTML half of the second-wave letter. Same
// words as the text half; the pixel sits last for the same reasons as in
// ComposeReactivationHTML.
func ComposeReactivationFixHTML(planName, expires string, promoLink, pixelURL, heroURL string) string {
	var b strings.Builder
	b.WriteString(`<div style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;font-size:15px;line-height:1.55;color:#111827;max-width:600px">`)
	writeMailHero(&b, heroURL, promoLink, "Теперь деплой по ссылке на любой Git")
	b.WriteString(`<p>Вы активировали тариф в Dada Cloud, но приложение так и не выехало.</p>`)
	b.WriteString(`<p>Скорее всего, споткнулись об нас: подключить репозиторий не с GitHub было нельзя, кнопка была, а за ней ничего. Это уже починено.</p>`)
	b.WriteString(`<p>Теперь достаточно вставить <b>ссылку на репозиторий</b> — GitHub, GitLab, любой публичный Git. Токен не нужен, приватный тоже можно, с токеном.</p>`)
	if expires != "" {
		fmt.Fprintf(&b, `<p>Тариф %s ещё действует до %s, ничего заново активировать не надо.</p>`,
			html.EscapeString(planName), html.EscapeString(expires))
	} else {
		fmt.Fprintf(&b, `<p>Тариф %s ещё действует, ничего заново активировать не надо.</p>`,
			html.EscapeString(planName))
	}
	fmt.Fprintf(&b, `<p><a href="%s" style="display:inline-block;background:#2563eb;color:#ffffff;text-decoration:none;padding:12px 22px;border-radius:8px;font-weight:600">Задеплоить по ссылке</a></p>`,
		html.EscapeString(promoLink))
	fmt.Fprintf(&b, `<p style="font-size:13px;color:#6b7280">Если кнопка не нажимается, откройте ссылку: <a href="%s">%s</a></p>`,
		html.EscapeString(promoLink), html.EscapeString(promoLink))
	b.WriteString(`<p>Если снова что-то упрётся — просто ответьте на это письмо.</p>`)
	if pixelURL != "" {
		fmt.Fprintf(&b, `<img src="%s" width="1" height="1" alt="" style="display:block;width:1px;height:1px;border:0">`,
			html.EscapeString(pixelURL))
	}
	b.WriteString(`</div>`)
	return b.String()
}

// Cause-kind values for the crash alert's cause_kind field (console banner
// and app_health_alerts.cause_kind). Empty/absent means unknown -- an app_code
// signature carries no ambiguity so ClassifyCrashCause never returns "".
const (
	CauseKindAppCode          = "app_code"
	CauseKindPlatformNetwork  = "platform_network"
	CauseKindPlatformStorage  = "platform_storage"
	CauseKindPlatformRegistry = "platform_registry"
	CauseKindResourceLimit    = "resource_limit"
	CauseKindNeedsArgs        = "app_needs_args"
)

// needsArgsCrashText is the cause_kind=CauseKindNeedsArgs verdict: the
// container did start, ran the program, and the program refused to run because
// its command line was empty. Nothing is broken -- the archive holds a CLI
// tool, and the platform starts an app the only way it can start one, with no
// arguments. Worded like resourceLimitCrashText: neither "your code is broken"
// nor "we are broken", because neither is true, and the way out is a change of
// shape (an entrypoint that runs without arguments), not a bug fix.
//
// Live case 2026-08-13: an uploaded archive whose entrypoint was
// argparse-based exited with "the following arguments are required" on every
// restart. The console showed the bare kube reason "Error" and no verdict at
// all, so the owner saw an app that crashloops forever for no stated reason.
const needsArgsCrashText = "Программа завершилась сразу после старта: она ждёт аргументы командной строки (CLI-скрипт), а платформа запускает приложение без аргументов. Это не сбой платформы и не ошибка в коде — нужен запуск, который работает без аргументов: задайте значения по умолчанию, читайте их из переменных окружения или поднимите сервер."

// needsArgsCrashSignatures are lines that only an argument parser prints when
// it refuses an empty command line. Every pattern here must be impossible to
// produce by accident in normal application output, same bar as
// platformCrashSignatures: a false "your app is a CLI" is as bad as a false
// "the platform broke". Covers argparse (Python), click/typer (Python),
// commander (Node) and cobra (Go).
var needsArgsCrashSignatures = []string{
	"the following arguments are required",
	"error: unrecognized arguments",
	"Error: Missing argument",
	"Error: Missing option",
	"error: required option",
	"error: missing required argument",
	`Error: required flag(s)`,
	"error: accepts 1 arg(s), received 0",
}

// resourceLimitCrashText is the cause_kind=CauseKindResourceLimit verdict
// shown for an OOMKilled container. This is deliberately worded as neither
// "your code is broken" nor "the platform is broken": the kernel killed the
// process for exceeding the memory ceiling attached to the app's plan, which
// is a capacity fact, not a bug on either side.
const resourceLimitCrashText = "Контейнер был остановлен нашей платформой: приложение превысило лимит памяти, выделенный по тарифу. Это не ошибка в коде — нужен профиль с большим лимитом или меньше потребление памяти."

// registryCrashText is the cause_kind=CauseKindPlatformRegistry verdict shown
// when the container never started at all because its image could not be
// pulled (ImagePullBackOff/ErrImagePull). This is a registry/delivery problem
// on our side, not a bug in the app's own code -- unlike a CrashLoopBackOff,
// where the container DID start and then failed. Worded to make that
// distinction explicit, mirroring resourceLimitCrashText's "neither side is
// broken by accident" precision for the case where the fault genuinely is
// ours.
const registryCrashText = "Контейнер не запускался вообще: платформа не смогла скачать образ приложения из реестра. Это не ошибка в вашем коде — сбой на нашей стороне, повторный деплой обычно чинит его."

// crashLogSignature is one entry in the ordered pattern table ClassifyCrashLog
// walks: pattern is matched with strings.Contains against the log excerpt,
// label is the human hint appended to the pattern name in the parenthetical.
type crashLogSignature struct {
	pattern string
	label   string
}

// platformCrashSignature is one entry in platformCrashSignatures: pattern is
// matched with strings.Contains, kind is the cause_kind value it proves, text
// is the full Russian sentence shown to the owner (unlike crashLogSignature's
// label, this is the whole message, not a parenthetical -- the platform text
// makes an unambiguous, standalone claim about which side broke).
type platformCrashSignature struct {
	pattern string
	kind    string
	text    string
}

// platformCrashSignatures is checked BEFORE pythonCrashSignatures and
// nodeCrashSignatures by ClassifyCrashCause: a platform failure (network path
// to a cluster-internal service, a full or unreadable volume) can surface
// inside a language runtime's own traceback -- e.g. psycopg raising
// OperationalError("No route to host") prints a normal Python traceback, and
// the Python table's first entry (the bare "Traceback (most recent call
// last)" header) would otherwise match first and tell the owner their own
// code is at fault for something the platform's network broke. Kept
// deliberately small: every pattern here must be a string only OUR
// infrastructure produces, never one an application can also print on its
// own, because a false "it's the platform" is exactly as bad as a false
// "it's your code".
var platformCrashSignatures = []platformCrashSignature{
	{
		pattern: "No route to host",
		kind:    CauseKindPlatformNetwork,
		text:    "Судя по логам, это похоже на сбой сети нашей платформы (нет маршрута до сервиса), а не на ошибку в коде приложения.",
	},
	{
		pattern: "no space left on device",
		kind:    CauseKindPlatformStorage,
		text:    "Судя по логам, это похоже на нехватку места на томе нашей платформы, а не на ошибку в коде приложения.",
	},
	{
		pattern: "Input/output error",
		kind:    CauseKindPlatformStorage,
		text:    "Судя по логам, это похоже на сбой тома нашей платформы (ошибка ввода-вывода), а не на ошибку в коде приложения.",
	},
}

// pythonCrashSignatures, nodeCrashSignatures and genericCrashSignatures are
// checked in this order by ClassifyCrashLog; the first match wins.
var pythonCrashSignatures = []crashLogSignature{
	{pattern: "Traceback (most recent call last)", label: "Python"},
	{pattern: "ModuleNotFoundError", label: "Python"},
	{pattern: "ImportError", label: "Python"},
	{pattern: "SyntaxError", label: "Python"},
	{pattern: "AttributeError", label: "Python"},
	{pattern: "NameError", label: "Python"},
}

var nodeCrashSignatures = []crashLogSignature{
	{pattern: "Cannot find module", label: "Node.js"},
	{pattern: "SyntaxError:", label: "Node.js"},
	{pattern: "ReferenceError", label: "Node.js"},
}

// ClassifyCrashCause looks at a crashed container's log excerpt and returns
// both a machine-readable cause_kind and the matching human Russian text.
// platformCrashSignatures is checked FIRST, ahead of the app_code language
// tables, so a platform failure (network, storage) wrapped in a user-language
// traceback is never labelled as the owner's own code -- see
// platformCrashSignatures for why that ordering matters. Everything the
// language tables recognize is app_code. Returns ("", "") when nothing
// recognizable matched, so callers can omit the line entirely rather than
// show a wrong guess.
func ClassifyCrashCause(excerpt string) (kind, text string) {
	for _, sig := range platformCrashSignatures {
		if strings.Contains(excerpt, sig.pattern) {
			return sig.kind, sig.text
		}
	}
	for _, pattern := range needsArgsCrashSignatures {
		if strings.Contains(excerpt, pattern) {
			return CauseKindNeedsArgs, needsArgsCrashText
		}
	}
	for _, sig := range pythonCrashSignatures {
		if strings.Contains(excerpt, sig.pattern) {
			return CauseKindAppCode, fmt.Sprintf("Судя по логам, это ошибка в коде приложения (%s).", sig.label)
		}
	}
	for _, sig := range nodeCrashSignatures {
		if strings.Contains(excerpt, sig.pattern) {
			return CauseKindAppCode, fmt.Sprintf("Судя по логам, это ошибка в коде приложения (%s).", sig.label)
		}
	}
	if strings.Contains(excerpt, "panic:") {
		return CauseKindAppCode, "Судя по логам, это похоже на ошибку в коде приложения."
	}
	return "", ""
}

// ClassifyCrashLog is ClassifyCrashCause with the cause_kind dropped, kept for
// callers that only ever showed the human text (the alert email body).
func ClassifyCrashLog(excerpt string) string {
	_, text := ClassifyCrashCause(excerpt)
	return text
}

// ClassifyCrashCauseWithReason is ClassifyCrashCause plus the one signal the
// log text itself cannot carry: the container's kube-reported termination
// reason. A reason of "OOMKilled" is an authoritative platform fact (the
// kernel/kubelet killed the process for exceeding its memory ceiling) and
// must win outright over whatever the log excerpt happens to contain --
// otherwise a coincidental language-runtime line in the same log (e.g. a
// half-printed Node.js stack as the process died) gets classified app_code
// and the owner is told their code is broken when the real cause is the
// plan's memory limit. Every other reason is unaffected: this delegates
// straight to ClassifyCrashCause, so passing an empty or unrecognized reason
// reproduces the old text-only behavior exactly. The literal "OOMKilled"
// string mirrors the kube API's own TerminationReason value (see
// api.reasonOOMKilled); notify does not import the api package to avoid a
// cycle, so the string is duplicated deliberately rather than shared.
//
// ImagePullBackOff/ErrImagePull get the same authoritative-kube-fact
// treatment as OOMKilled, for a different reason: the log excerpt is always
// empty for these two (the container never started, so nothing was ever
// written to its stdout/stderr), so ClassifyCrashCause would fall through to
// its "" default and the owner would be told nothing beyond the bare kube
// reason string. Without this branch a registry-side failure (ours) reads
// exactly like an unclassified crash, which is indistinguishable in the
// email from the owner's own code failing to start.
func ClassifyCrashCauseWithReason(reason, excerpt string) (kind, text string) {
	switch reason {
	case "OOMKilled":
		return CauseKindResourceLimit, resourceLimitCrashText
	case "ImagePullBackOff", "ErrImagePull":
		return CauseKindPlatformRegistry, registryCrashText
	}
	return ClassifyCrashCause(excerpt)
}

// causeLineMaxRunes bounds ExtractCauseLine's return value. Counted in runes,
// not bytes: a Python traceback line can carry Cyrillic text from a
// translated dependency message, and truncating on a byte boundary there
// would cut a multi-byte UTF-8 rune in half and corrupt the tail of the
// string.
const causeLineMaxRunes = 300

// crashLineSignaturePatterns flattens platformCrashSignatures,
// pythonCrashSignatures and nodeCrashSignatures plus the bare "panic:" match
// ClassifyCrashCause also checks, so ExtractCauseLine walks the exact same
// signature set ClassifyCrashCause does instead of maintaining a second copy
// that could silently drift out of sync with it. Without the platform
// patterns here, a platform_network/platform_storage failure would classify
// correctly but still show no cause_line -- the console banner would state a
// cause with no evidence line under it.
func crashLineSignaturePatterns() []string {
	patterns := make([]string, 0, len(platformCrashSignatures)+len(needsArgsCrashSignatures)+len(pythonCrashSignatures)+len(nodeCrashSignatures)+1)
	for _, sig := range platformCrashSignatures {
		patterns = append(patterns, sig.pattern)
	}
	patterns = append(patterns, needsArgsCrashSignatures...)
	for _, sig := range pythonCrashSignatures {
		patterns = append(patterns, sig.pattern)
	}
	for _, sig := range nodeCrashSignatures {
		patterns = append(patterns, sig.pattern)
	}
	patterns = append(patterns, "panic:")
	return patterns
}

// pythonTracebackHeader is the first line CPython prints for an uncaught
// exception. It is deliberately NOT usable as a cause line on its own: it
// names no exception type and no message, so storing it as the cause tells
// the owner only "python died", which ClassifyCrashLog already says.
const pythonTracebackHeader = "Traceback (most recent call last)"

// IsUnusableCauseLine reports whether an already-stored cause line carries no
// information about the failure and should therefore be re-derived from a
// fresh log read. Today that is exactly the bare Python traceback header,
// which older builds of ExtractCauseLine could store as the cause; callers
// keep such a row from being treated as "cause already known" so the fixed
// extractor gets a chance to replace it instead of the stale value sticking
// for as long as the app keeps crashlooping.
func IsUnusableCauseLine(causeLine string) bool {
	return strings.Contains(causeLine, pythonTracebackHeader)
}

// pythonExceptionLinePattern matches the shape of CPython's final traceback
// line: a dotted identifier (the exception type, possibly module-qualified)
// optionally followed by ": message". Frames are indented and therefore never
// reach this check; the pattern exists to reject unindented application
// output that happens to trail a traceback, so an arbitrary log line can
// never be presented to the owner as the crash cause.
var pythonExceptionLinePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*(:.*)?$`)

// pythonTracebackException returns the exception line of the LAST traceback
// in the excerpt, or "" when the excerpt has no traceback or the traceback is
// cut off before its exception line (a tail window can start mid-frames).
//
// This exists because the signature-table scan alone cannot find the cause of
// an exception type nobody thought to list: P1 live case was a RuntimeError
// from a missing S3 model path, where the only line matching any known
// signature was the traceback header itself, so the stored cause_line — shown
// in the console banner, mailed to the owner, and fed to auto-fix as evidence
// — was the literal string "Traceback (most recent call last):". CPython's
// format is authoritative here: after the header come indented frames, and
// the last unindented line is the exception. Reading the format instead of
// enumerating exception types covers every type, including ones defined by
// the application itself.
func pythonTracebackException(excerpt string) string {
	lines := strings.Split(excerpt, "\n")
	header := -1
	for i, line := range lines {
		if strings.Contains(line, pythonTracebackHeader) {
			header = i
		}
	}
	if header < 0 {
		return ""
	}
	for i := len(lines) - 1; i > header; i-- {
		line := strings.TrimRight(lines[i], " \t\r")
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		if strings.Contains(line, pythonTracebackHeader) {
			continue
		}
		if pythonExceptionLinePattern.MatchString(line) {
			return line
		}
	}
	return ""
}

// ExtractCauseLine picks the single most telling line out of a crashed
// container's log excerpt. A Python traceback is read by its own format
// first (pythonTracebackException), because that finds the real exception
// even when its type is absent from the signature tables; everything else
// falls back to the LAST line containing one of the known error signatures
// (crashLineSignaturePatterns), since a stack dump lists causes top to bottom
// and the final matching line is usually the one closest to the failure. The
// bare traceback header is never a valid answer in either path.
//
// Returns "" when nothing matches — this must never guess or fall back to an
// arbitrary line, because a wrong "cause" shown next to a crash is worse than
// no cause at all (same rule ClassifyCrashLog already follows). The result is
// truncated to causeLineMaxRunes, measured in runes so a UTF-8 line is never
// cut mid-rune.
func ExtractCauseLine(excerpt string) string {
	if excerpt == "" {
		return ""
	}
	best := pythonTracebackException(excerpt)
	if best == "" {
		patterns := crashLineSignaturePatterns()
		for _, line := range strings.Split(excerpt, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.Contains(trimmed, pythonTracebackHeader) {
				continue
			}
			for _, p := range patterns {
				if strings.Contains(trimmed, p) {
					best = trimmed
					break
				}
			}
		}
	}
	if best == "" {
		return ""
	}
	if runes := []rune(best); len(runes) > causeLineMaxRunes {
		return string(runes[:causeLineMaxRunes])
	}
	return best
}

// ComposeAppAlert builds the subject and plaintext body for a silent-crash
// alert: the owner's app is stuck in CrashLoopBackOff/OOMKilled/ImagePullBackOff
// and would otherwise go unnoticed until the owner happens to open the
// console. logExcerpt is the best-effort last lines of the crashed container's
// log (may be empty when the cluster read failed or there was nothing to
// read); consoleLink deep-links straight to the app in the console. codeHint
// is the optional ClassifyCrashLog result (may be ""); agentURL deep-links to
// the console's AI agent panel for this app.
//
// The opening sentence is reason-aware: for ImagePullBackOff/ErrImagePull the
// container never started at all, so "перезапускается" (keeps restarting)
// would be a false claim -- that exact wording was previously used
// unconditionally and made a registry failure on our side read as the
// owner's own app crashing.
func ComposeAppAlert(appName, reason, podName, logExcerpt, consoleLink, codeHint, agentURL string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: %s не работает (%s)", appName, reason)
	var b strings.Builder
	switch reason {
	case "ImagePullBackOff", "ErrImagePull":
		fmt.Fprintf(&b, "Приложение %s не может запуститься: платформа не смогла скачать его образ из реестра.\n\n", appName)
	default:
		fmt.Fprintf(&b, "Приложение %s перезапускается и, похоже, не поднимается.\n\n", appName)
	}
	fmt.Fprintf(&b, "Причина: %s\n", reason)
	fmt.Fprintf(&b, "Под: %s\n\n", podName)
	if codeHint != "" {
		b.WriteString(codeHint)
		b.WriteString("\n\n")
	}
	if logExcerpt != "" {
		b.WriteString("Последние строки лога:\n")
		b.WriteString(logExcerpt)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Открыть в консоли: %s\n\n", consoleLink)
	fmt.Fprintf(&b, "Спросите AI-агента в консоли - он видит логи вашего приложения и поможет разобраться: %s\n\n", agentURL)
	b.WriteString("Это письмо приходит не чаще раза в 24 часа на приложение.\n")
	return subject, b.String()
}

// ComposeVolumeAlert builds the subject and plaintext body for a volume-fill
// alert: the owner's app is at or above appVolumeAlertThreshold on its
// persistent volume and would otherwise fill silently until an out-of-space
// write crashes the app (P2, real incident: fonbet-value hit 100% and
// CrashLooped for about a day before anyone noticed). declaredSize is the
// app's declared volume size (e.g. "10Gi"), or "" when it could not be read;
// consoleLink deep-links to the app's Storage settings tab.
func ComposeVolumeAlert(appName string, ratio float64, declaredSize, consoleLink string) (subject, body string) {
	percent := ratio * 100
	subject = fmt.Sprintf("Dada Cloud: том приложения %s заполнен на %.0f%%", appName, percent)
	var b strings.Builder
	fmt.Fprintf(&b, "Постоянный том приложения %s заполнен на %.0f%%.\n\n", appName, percent)
	if declaredSize != "" {
		fmt.Fprintf(&b, "Объявленный размер: %s\n\n", declaredSize)
	}
	b.WriteString("Если том заполнится полностью, приложение может перестать работать (нет места для записи).\n\n")
	fmt.Fprintf(&b, "Открыть хранилище в консоли: %s\n\n", consoleLink)
	b.WriteString("Увеличить том можно там же, либо выгрузить и почистить данные через экспорт тома.\n\n")
	b.WriteString("Это письмо приходит не чаще раза в 24 часа на приложение.\n")
	return subject, b.String()
}

// ComposeDatabaseQuotaWarning warns an owner that a managed database is
// approaching its tier's storage quota, while writes still work. It names the
// two ways out (free space, or move up a plan) because the next step the
// platform takes on its own — read-only — is not one the owner can undo by
// waiting.
func ComposeDatabaseQuotaWarning(dbName, tier string, usedGB, limitGB float64, consoleLink string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: база %s заняла %.0f%% квоты", dbName, usedGB/limitGB*100)
	var b strings.Builder
	fmt.Fprintf(&b, "База данных %s занимает %.1f ГБ из %.0f ГБ, доступных на тарифе (квота %s).\n\n", dbName, usedGB, limitGB, tier)
	b.WriteString("Когда база дойдёт до квоты, платформа переведёт её в режим только для чтения: данные останутся на месте и будут читаться, но запись перестанет проходить. Это делается автоматически, чтобы одна база не съела диск, общий с базами других проектов.\n\n")
	b.WriteString("Что можно сделать: освободить место (удалить лишние данные, VACUUM FULL) или перейти на тариф с большей квотой.\n\n")
	fmt.Fprintf(&b, "Открыть базы в консоли: %s\n\n", consoleLink)
	b.WriteString("Это письмо приходит не чаще раза в 24 часа на базу.\n")
	return subject, b.String()
}

// ComposeDatabaseQuotaEnforced reports that the quota has actually been
// applied. It states plainly that nothing was deleted and how the state is
// released, because the first thing an owner assumes when writes start failing
// is data loss.
func ComposeDatabaseQuotaEnforced(dbName, state string, usedGB, limitGB float64, consoleLink string) (subject, body string) {
	if state == "frozen" {
		subject = fmt.Sprintf("Dada Cloud: база %s отключена по квоте", dbName)
	} else {
		subject = fmt.Sprintf("Dada Cloud: база %s переведена в режим только для чтения", dbName)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "База данных %s занимает %.1f ГБ при квоте %.0f ГБ.\n\n", dbName, usedGB, limitGB)
	if state == "frozen" {
		b.WriteString("База продолжала расти после перевода в режим только для чтения, поэтому платформа закрыла подключения к ней. Данные не удалены.\n\n")
	} else {
		b.WriteString("Запись в базу отключена, чтение работает. Данные не удалены и не тронуты.\n\n")
	}
	b.WriteString("Ограничение снимается автоматически, как только база опустится ниже 90% квоты, либо сразу после перехода на тариф с большей квотой.\n\n")
	fmt.Fprintf(&b, "Открыть базы в консоли: %s\n", consoleLink)
	return subject, b.String()
}

// ComposeDatabaseQuotaGrace is the letter for a database that was already over
// its quota the moment the quota first applied to it. That owner did not watch
// a limit approach -- the limit arrived under data that was already there -- so
// the letter leads with "nothing has changed yet", names the deadline, and
// lists the three ways out before it expires.
func ComposeDatabaseQuotaGrace(dbName, tier string, usedGB, limitGB float64, graceUntilUTC time.Time, consoleLink string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: база %s превышает квоту тарифа, есть сутки на решение", dbName)
	var b strings.Builder
	fmt.Fprintf(&b, "База данных %s занимает %.1f ГБ при квоте %.0f ГБ на тарифе (%s).\n\n", dbName, usedGB, limitGB, tier)
	b.WriteString("Прямо сейчас ничего не изменилось: база работает на чтение и на запись как раньше.\n\n")
	fmt.Fprintf(&b, "До %s включительно действует отсрочка. После неё платформа переведёт базу в режим только для чтения: данные останутся на месте и будут читаться, но запись перестанет проходить.\n\n",
		graceUntilUTC.Format("02.01.2006 15:04 UTC"))
	b.WriteString("Что можно сделать за это время:\n")
	b.WriteString("  1. Перейти на тариф с большей квотой.\n")
	b.WriteString("  2. Удалить лишние данные и вернуть место (VACUUM FULL / pg_repack).\n")
	b.WriteString("  3. Выгрузить старые данные в архив: платформа умеет выносить историю append-only таблиц в Parquet на S3 и подчищать ровно эти строки. Данные остаются вашими и читаются любым инструментом.\n\n")
	b.WriteString("Резервные копии базы делаются как обычно и не затрагиваются ничем из перечисленного.\n\n")
	fmt.Fprintf(&b, "Открыть базы в консоли: %s\n", consoleLink)
	return subject, b.String()
}

// ComposeDatabaseQuotaGraceEnding is the last letter before writes stop.
//
// It exists because the grace letter arrives a full day early, which is long
// enough to be filed and forgotten. This one is deliberately short: the
// deadline, what happens at it, and the three ways out in one line each.
func ComposeDatabaseQuotaGraceEnding(dbName string, usedGB, limitGB float64, graceUntilUTC time.Time, hoursLeft int, consoleLink string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: у базы %s осталось %d ч до перевода в режим только для чтения", dbName, hoursLeft)
	var b strings.Builder
	fmt.Fprintf(&b, "База данных %s занимает %.1f ГБ при квоте %.0f ГБ.\n\n", dbName, usedGB, limitGB)
	fmt.Fprintf(&b, "Отсрочка заканчивается %s. После этого запись в базу отключится, чтение продолжит работать. Данные не удаляются.\n\n",
		graceUntilUTC.Format("02.01.2006 15:04 UTC"))
	b.WriteString("Успеть можно так:\n")
	b.WriteString("  1. Перейти на тариф с большей квотой — ограничение снимется сразу.\n")
	b.WriteString("  2. Удалить лишние данные и вернуть место (VACUUM FULL / pg_repack).\n")
	b.WriteString("  3. Выгрузить историю в архив: платформа вынесет старые строки append-only таблицы в Parquet на S3 и подчистит ровно их.\n\n")
	b.WriteString("Резервные копии базы делаются как обычно и содержат всё.\n\n")
	fmt.Fprintf(&b, "Открыть базы в консоли: %s\n", consoleLink)
	return subject, b.String()
}

// ComposeDatabaseArchiveDone reports a finished archive.
//
// The letter has one job beyond "it worked": telling the owner that the rows
// are still theirs and how to read them without the platform. Someone whose
// data was moved by an automatic process needs the path back to it in the same
// message, not in documentation they have to go find.
func ComposeDatabaseArchiveDone(dbName, table, cutoff string, rows int64, freedGB float64, s3URI string, auto bool, consoleLink string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: история таблицы %s выгружена в архив", table)
	var b strings.Builder
	if auto {
		fmt.Fprintf(&b, "База %s упёрлась в квоту тарифа, поэтому платформа вынесла старые строки таблицы %s в архив.\n\n", dbName, table)
	} else {
		fmt.Fprintf(&b, "Архивирование таблицы %s в базе %s завершено.\n\n", table, dbName)
	}
	fmt.Fprintf(&b, "Уехало строк: %d (всё, что старше %s).\n", rows, cutoff)
	fmt.Fprintf(&b, "Освободилось на диске: %.1f ГБ.\n\n", freedGB)
	b.WriteString("Данные не потеряны. Они лежат в вашем S3-бакете в формате Parquet — это открытый формат, читается чем угодно:\n")
	fmt.Fprintf(&b, "  %s\n\n", s3URI)
	fmt.Fprintf(&b, "DuckDB:  SELECT * FROM read_parquet('%s');\n", s3URI)
	fmt.Fprintf(&b, "pandas:  pandas.read_parquet('%s')\n\n", s3URI)
	b.WriteString("Кроме архива, эти строки остаются в резервных копиях базы: бэкапы делались как обычно и ничем из перечисленного не затронуты.\n\n")
	fmt.Fprintf(&b, "Открыть базы в консоли: %s\n", consoleLink)
	return subject, b.String()
}

// ComposeDatabaseQuotaReleased tells the owner the limit is gone, so nobody
// keeps working around a restriction that no longer exists.
func ComposeDatabaseQuotaReleased(dbName string, usedGB, limitGB float64, consoleLink string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: ограничение с базы %s снято", dbName)
	var b strings.Builder
	fmt.Fprintf(&b, "База данных %s снова доступна на запись: %.1f ГБ из %.0f ГБ квоты.\n\n", dbName, usedGB, limitGB)
	fmt.Fprintf(&b, "Открыть базы в консоли: %s\n", consoleLink)
	return subject, b.String()
}

// autoscaleReasonRU renders the tripped dimension in the same words the
// console uses, so an owner reading the email and then opening the app sees
// one vocabulary rather than two.
func autoscaleReasonRU(reason string) string {
	if reason == "memory" {
		return "нехватка памяти"
	}
	return "нехватка процессорного времени"
}

// ComposeAutoscaleCeiling reports starvation at the top of the ladder, where
// the platform stops resizing on purpose. It says plainly that NOTHING was
// changed and why, and points at the likelier cause: past this size the
// problem is usually a leak or a runaway loop, not a need for more hardware.
func ComposeAutoscaleCeiling(appName, profile, reason string, ratio float64, consoleLink string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: приложению %s не хватает ресурсов на максимальном профиле", appName)
	var b strings.Builder
	fmt.Fprintf(&b, "Приложение %s испытывает нехватку ресурсов, но уже работает на максимальном профиле (%s), поэтому платформа НИЧЕГО не меняла.\n\n", appName, profile)
	fmt.Fprintf(&b, "Причина: %s (показатель %.0f%%).\n\n", autoscaleReasonRU(reason), ratio*100)
	b.WriteString("На таком размере причина обычно не в нехватке железа, а в утечке памяти или зациклившейся задаче в самом приложении. Стоит посмотреть логи и метрики.\n\n")
	fmt.Fprintf(&b, "Открыть приложение в консоли: %s\n\n", consoleLink)
	b.WriteString("Это письмо приходит не чаще раза в 6 часов на приложение.\n")
	return subject, b.String()
}

// Dada Box notifications.
//
// Every one of these says WHAT WILL HAPPEN TO THE DATA, in the first paragraph,
// because that is the only question a customer actually has when a box is
// suspended or about to be reaped. A box is a body the customer's agent worked in;
// "we stopped it" and "we deleted it" are entirely different pieces of news and an
// email that leaves that ambiguous is worse than no email.

// ComposeBoxSpendCapWarning is the heads-up at boxSpendCapWarnRatio of the cap:
// nothing has happened yet, and there are two ways to keep it that way.
func ComposeBoxSpendCapWarning(boxName string, spentRub, capRub float64) (subject, body string) {
	subject = fmt.Sprintf("Dada Box: бокс %s израсходовал %.0f%% лимита", boxName, spentRub/capRub*100)
	var b strings.Builder
	fmt.Fprintf(&b, "Бокс %s израсходовал %.2f ₽ из лимита %.2f ₽ за текущий месяц.\n\n", boxName, spentRub, capRub)
	b.WriteString("Пока ничего не произошло: бокс работает, данные на месте.\n\n")
	b.WriteString("При достижении лимита бокс будет ПРИОСТАНОВЛЕН (усыплён), а не удалён: диск, установленные пакеты и подключённые базы сохраняются, решение остаётся за вами.\n\n")
	b.WriteString("Варианты: поднять лимит расходов у бокса, либо усыпить его самостоятельно, когда работа закончена. Минуты простоя не тарифицируются вообще.\n\n")
	b.WriteString("Это письмо приходит один раз на бокс.\n")
	return subject, b.String()
}

// ComposeBoxSpendCapStopped is sent when the cap suspended the box. It leads with
// the fact that nothing was destroyed, because that is what the customer will
// assume happened.
func ComposeBoxSpendCapStopped(boxName string, spentRub, capRub float64) (subject, body string) {
	subject = fmt.Sprintf("Dada Box: бокс %s приостановлен по лимиту расходов", boxName)
	var b strings.Builder
	fmt.Fprintf(&b, "Бокс %s достиг лимита расходов (%.2f ₽ из %.2f ₽) и был ПРИОСТАНОВЛЕН.\n\n", boxName, spentRub, capRub)
	b.WriteString("Данные не удалены. Диск бокса, установленные пакеты и подключённые базы и бакеты на месте: подключённые ресурсы вообще живут вне бокса и лимитом не затрагиваются.\n\n")
	b.WriteString("Чтобы продолжить работу, поднимите лимит расходов бокса — после этого бокс можно разбудить (resume), и это тот же бокс с тем же состоянием.\n\n")
	b.WriteString("Если работа в боксе оказалась нужна надолго — её можно кристаллизовать в постоянную VM с доменом: месячная цена вместо поминутной, состояние переезжает как есть.\n")
	return subject, b.String()
}

// ComposeBoxDiskAccrualWarning is the only Dada Box email that announces a coming
// deletion from the billing side: a box asleep so long that its rootfs alone has
// accrued a multiple of its spend cap.
func ComposeBoxDiskAccrualWarning(boxName string, diskSpentRub, capRub float64, grace time.Duration) (subject, body string) {
	subject = fmt.Sprintf("Dada Box: бокс %s будет удалён через %.0f ч", boxName, grace.Hours())
	var b strings.Builder
	fmt.Fprintf(&b, "Спящий бокс %s продолжает занимать диск: %.2f ₽ хранения при лимите расходов %.2f ₽.\n\n", boxName, diskSpentRub, capRub)
	fmt.Fprintf(&b, "Если ничего не изменится, через %.0f часов бокс будет УДАЛЁН вместе с его диском. Это необратимо для всего, что живёт только внутри бокса.\n\n", grace.Hours())
	b.WriteString("Что можно сделать сейчас: разбудить бокс и забрать нужное; поднять лимит расходов, если бокс ещё нужен; или кристаллизовать его в постоянную VM — тогда состояние сохраняется, а тарификация становится месячной.\n\n")
	b.WriteString("Подключённые базы и бакеты живут вне бокса и удалены НЕ будут.\n")
	return subject, b.String()
}

// ComposeBoxReapWarning is the sleep-reaper's warning. attemptsLeft distinguishes
// the first notice from the last one, because "we will delete this" read twice with
// identical wording is read as a duplicate and ignored.
//
// THIS IS THE CRYSTALLIZATION UPSELL MOMENT, and it is deliberate rather than
// opportunistic: a customer who has left a box asleep for two days has a prototype
// that survived. That is exactly the population the product's monetization ladder
// is built for, and this is the only moment we can be sure they still care about
// the contents — so the email offers promotion to a permanent VM instead of only
// announcing a deletion. An email that just says "we are deleting your work" wastes
// the one conversation the funnel exists to have.
func ComposeBoxReapWarning(boxName string, asleepHours, deleteInHours float64, final bool) (subject, body string) {
	prefix := "Dada Box"
	if final {
		prefix = "Dada Box (последнее предупреждение)"
	}
	subject = fmt.Sprintf("%s: спящий бокс %s будет удалён через %.0f ч", prefix, boxName, deleteInHours)
	var b strings.Builder
	fmt.Fprintf(&b, "Бокс %s спит уже %.0f часов.\n\n", boxName, asleepHours)
	fmt.Fprintf(&b, "Через %.0f часов он будет удалён вместе с диском. Мы храним спящий бокс 72 часа — этого достаточно, чтобы вернуться и забрать нужное.\n\n", deleteInHours)
	b.WriteString("Если прототип в этом боксе выжил — его можно кристаллизовать в постоянную VM: тот же диск, те же пакеты, те же подключённые базы и переменные, плюс домен и HTTPS. Поминутная тарификация меняется на месячную.\n\n")
	b.WriteString("Если бокс больше не нужен — ничего делать не надо.\n\n")
	b.WriteString("Подключённые базы и бакеты живут вне бокса и удалены НЕ будут.\n")
	return subject, b.String()
}

// ComposeNoOwnerFallback wraps an already-composed alert subject/body for the
// operator-fallback case (P1-ALERT-OWNERLESS-DROP): the resolver chain found
// no reachable owner (no owner_id, no Owner/Admin member, no personal-org
// username match), so the alert routes to the operator mailbox instead of
// being silently dropped. The wrapped copy states plainly, up front, that
// this is not a normal owner alert — the operator needs to see at a glance
// that a project has drifted into an unreachable-owner state, not just read
// another crash/volume email as if it reached the customer.
func ComposeNoOwnerFallback(projectID, projectName, origSubject, origBody string) (subject, body string) {
	subject = fmt.Sprintf("[БЕЗ ВЛАДЕЛЬЦА] %s", origSubject)
	var b strings.Builder
	b.WriteString("ВНИМАНИЕ: у проекта не найден достижимый владелец (нет owner_id, нет участника Owner/Admin, нет совпадения personal-org). Письмо отправлено оператору вместо клиента.\n")
	fmt.Fprintf(&b, "Проект: %s (id=%s)\n\n", projectName, projectID)
	b.WriteString(origBody)
	return subject, b.String()
}

// ComposeFeedback builds the operator notice for one in-product support
// ticket. It leads with the message itself: the operator's decision is
// "does this need me right now", and that is answered by the words the
// customer wrote, not by metadata. Sender/org/route follow so the ticket can
// be answered without opening anything, and appName is called out separately
// because it is the field that decides whether the auto-fix engine can be
// pointed at this ticket at all.
func ComposeFeedback(senderEmail, orgID, route, message, appName, adminLink string) (subject, body string) {
	who := senderEmail
	if who == "" {
		who = "аноним"
	}
	subject = fmt.Sprintf("Dada Cloud: обращение от %s", who)
	var b strings.Builder
	b.WriteString(message)
	b.WriteString("\n\n---\n")
	fmt.Fprintf(&b, "От: %s\n", who)
	if orgID != "" {
		fmt.Fprintf(&b, "Организация: %s\n", orgID)
	}
	if appName != "" {
		fmt.Fprintf(&b, "Приложение: %s\n", appName)
	}
	if route != "" {
		fmt.Fprintf(&b, "Страница: %s\n", route)
	}
	fmt.Fprintf(&b, "\nОбращения в консоли: %s\n", adminLink)
	return subject, b.String()
}

// ComposeAutofixReady tells the owner an auto-fix run finished and left a pull
// request waiting. Without this email the run is invisible: on prod three
// auto-fix PRs were opened and none was merged, because the only place the
// link appeared was a cloud_tasks row and a console panel nobody reloaded.
// The body says plainly that nothing was deployed -- an unexpected "we fixed
// your app" reads as "someone pushed to my repo" unless it says otherwise.
func ComposeAutofixReady(appName, prURL, consoleLink string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: готово исправление для %s", appName)
	var b strings.Builder
	fmt.Fprintf(&b, "AI разобрал проблему приложения %s и подготовил исправление.\n\n", appName)
	if prURL != "" {
		fmt.Fprintf(&b, "Pull request: %s\n\n", prURL)
	}
	b.WriteString("Ничего не задеплоено и в вашу основную ветку не записано: это отдельная ветка и PR. Посмотрите диф, и если он верный - влейте его, дальше платформа соберёт и выкатит как обычный пуш.\n\n")
	fmt.Fprintf(&b, "Приложение в консоли: %s\n", consoleLink)
	return subject, b.String()
}

// ComposeAutofixFailed is the operator-only counterpart. Six of the first nine
// prod auto-fix runs died on infrastructure (agent pod restart, git missing
// from PATH, a 500 from the model gateway) and every one of those failures was
// silent, so the feature looked unused rather than broken.
func ComposeAutofixFailed(appName, reason, consoleLink string) (subject, body string) {
	subject = fmt.Sprintf("[АВТОФИКС УПАЛ] %s", appName)
	var b strings.Builder
	fmt.Fprintf(&b, "Запуск авто-исправления для приложения %s завершился неудачей.\n\n", appName)
	if reason != "" {
		fmt.Fprintf(&b, "Причина: %s\n\n", reason)
	}
	fmt.Fprintf(&b, "Приложение в консоли: %s\n", consoleLink)
	return subject, b.String()
}

// Send delivers one message to a single recipient over SMTP with STARTTLS
// (net/smtp negotiates STARTTLS automatically when the server advertises it,
// as Postbox does on 587). Returns an error the caller logs and swallows.
func (n *Notifier) Send(to, subject, body string) error {
	if n == nil || to == "" {
		return nil
	}
	addr := fmt.Sprintf("%s:%d", n.host, n.port)
	msg := n.render(to, subject, body)
	var auth smtp.Auth
	if n.user != "" {
		auth = smtp.PlainAuth("", n.user, n.pass, n.host)
	}
	return smtp.SendMail(addr, auth, n.from, []string{to}, []byte(msg))
}

// SendHTML delivers one message that carries both a plain-text and an HTML
// body as multipart/alternative.
//
// Campaign mail needs the HTML part to carry the open pixel, and it needs the
// text part for the same reason it always did: a text-only client must still
// get a readable letter, and an HTML-only message scores worse with spam
// filters. The text body is not a fallback afterthought — it is the message,
// and the HTML part says the same words.
func (n *Notifier) SendHTML(to, subject, textBody, htmlBody string) error {
	if n == nil || to == "" {
		return nil
	}
	addr := fmt.Sprintf("%s:%d", n.host, n.port)
	msg := n.renderAlternative(to, subject, textBody, htmlBody)
	var auth smtp.Auth
	if n.user != "" {
		auth = smtp.PlainAuth("", n.user, n.pass, n.host)
	}
	return smtp.SendMail(addr, auth, n.from, []string{to}, []byte(msg))
}

// renderAlternative assembles a two-part multipart/alternative message. The
// HTML part comes last because clients render the final part they can display.
func (n *Notifier) renderAlternative(to, subject, textBody, htmlBody string) string {
	boundary := multipartBoundary()
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", n.from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", encodeHeader(subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary)
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	b.WriteString(encodeBody(textBody))
	fmt.Fprintf(&b, "\r\n--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	b.WriteString(encodeBody(htmlBody))
	fmt.Fprintf(&b, "\r\n--%s--\r\n", boundary)
	return b.String()
}

// multipartBoundary returns a delimiter that cannot occur inside the bodies.
// Random rather than fixed: a constant boundary that happens to appear in a
// user-supplied line would split the message at the wrong place.
func multipartBoundary() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "dada-boundary-fallback"
	}
	return "dada-" + hex.EncodeToString(buf)
}

// render assembles RFC-5322 headers + UTF-8 body.
func (n *Notifier) render(to, subject, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", n.from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", encodeHeader(subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")
	b.WriteString(encodeBody(body))
	return b.String()
}

// encodeBody quoted-printable-encodes a message body, which is what keeps a
// single long line from killing the whole letter.
//
// RFC 5321 caps a line at 1000 octets, and Postbox enforces it: it answered
// 500 "Line too long" and dropped the connection. Nine of the ten
// operator-fallback alerts in the last 30 days died that way — every one of
// them for profi-backend, whose crash excerpt carries an unwrapped traceback
// line. The body was previously written raw, with newlines merely converted to
// CRLF, so nothing bounded a line: an app whose log happened to be wide made
// the alert about it undeliverable. The same render path serves volume,
// autoscale, quota and box-reap alerts, so this was every alert waiting for a
// wide enough log line.
//
// Quoted-printable rather than manual wrapping: the encoder inserts soft line
// breaks that the receiving client removes, so the reader sees the original
// text, and it does so without ever splitting a multi-byte Cyrillic rune.
//
// On the encoder's own failure the raw body is returned: a letter that might
// be rejected still beats no letter at all.
func encodeBody(body string) string {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	var out bytes.Buffer
	w := quotedprintable.NewWriter(&out)
	if _, err := io.WriteString(w, normalized); err != nil {
		return strings.ReplaceAll(normalized, "\n", "\r\n")
	}
	if err := w.Close(); err != nil {
		return strings.ReplaceAll(normalized, "\n", "\r\n")
	}
	return out.String()
}

// encodeHeader RFC-2047 base64-encodes a header value when it contains
// non-ASCII (Russian subjects), so mail clients render Cyrillic correctly
// instead of mojibake.
func encodeHeader(s string) string {
	if isASCII(s) {
		return s
	}
	return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}
