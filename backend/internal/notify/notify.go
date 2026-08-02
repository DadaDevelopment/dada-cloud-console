// Package notify sends best-effort operator email over the shared Postbox
// SMTP relay (same credentials Keycloak uses for its own mail). Every send is
// fire-and-forget: callers launch it off the request's hot path and log
// errors instead of propagating them, so a mail outage never blocks a user
// action.
package notify

import (
	"encoding/base64"
	"fmt"
	"net/smtp"
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
// customer-facing payment-success email (YooKassa checkout, payments slice 1).
func ComposePaymentSuccess(planName, amountValue string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: оплата тарифа %s прошла успешно", planName)
	var b strings.Builder
	fmt.Fprintf(&b, "Спасибо! Платёж на тариф %s (%s ₽) успешно проведён.\n\n", planName, amountValue)
	b.WriteString("Новый тариф уже активен в консоли.\n")
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

// crashLogSignature is one entry in the ordered pattern table ClassifyCrashLog
// walks: pattern is matched with strings.Contains against the log excerpt,
// label is the human hint appended to the pattern name in the parenthetical.
type crashLogSignature struct {
	pattern string
	label   string
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

// ClassifyCrashLog looks at a crashed container's log excerpt for known
// application-code error signatures (as opposed to infra failures like
// OOMKilled or ImagePullBackOff) and returns a short Russian hint pointing
// the owner at their own code. Returns "" when nothing recognizable matched,
// so callers can omit the line entirely rather than show a wrong guess.
func ClassifyCrashLog(excerpt string) string {
	for _, sig := range pythonCrashSignatures {
		if strings.Contains(excerpt, sig.pattern) {
			return fmt.Sprintf("Судя по логам, это ошибка в коде приложения (%s).", sig.label)
		}
	}
	for _, sig := range nodeCrashSignatures {
		if strings.Contains(excerpt, sig.pattern) {
			return fmt.Sprintf("Судя по логам, это ошибка в коде приложения (%s).", sig.label)
		}
	}
	if strings.Contains(excerpt, "panic:") {
		return "Судя по логам, это похоже на ошибку в коде приложения."
	}
	return ""
}

// ComposeAppAlert builds the subject and plaintext body for a silent-crash
// alert: the owner's app is stuck in CrashLoopBackOff/OOMKilled/ImagePullBackOff
// and would otherwise go unnoticed until the owner happens to open the
// console. logExcerpt is the best-effort last lines of the crashed container's
// log (may be empty when the cluster read failed or there was nothing to
// read); consoleLink deep-links straight to the app in the console. codeHint
// is the optional ClassifyCrashLog result (may be ""); agentURL deep-links to
// the console's AI agent panel for this app.
func ComposeAppAlert(appName, reason, podName, logExcerpt, consoleLink, codeHint, agentURL string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: %s не работает (%s)", appName, reason)
	var b strings.Builder
	fmt.Fprintf(&b, "Приложение %s перезапускается и, похоже, не поднимается.\n\n", appName)
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

// autoscaleReasonRU renders the tripped dimension in the same words the
// console uses, so an owner reading the email and then opening the app sees
// one vocabulary rather than two.
func autoscaleReasonRU(reason string) string {
	if reason == "memory" {
		return "нехватка памяти"
	}
	return "нехватка процессорного времени"
}

// ComposeAutoscaleNotice reports a resize that ALREADY HAPPENED. It leads with
// the fact and the new size, because the owner's first question on seeing an
// unexpected rollout is "what changed and who changed it", and states the
// no-charge fact explicitly: a message about being given more hardware reads
// as a bill unless it says otherwise.
func ComposeAutoscaleNotice(appName, from, to, reason string, ratio float64, consoleLink string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: приложению %s увеличены ресурсы (%s → %s)", appName, from, to)
	var b strings.Builder
	fmt.Fprintf(&b, "Приложению %s не хватало ресурсов, поэтому платформа автоматически увеличила его профиль: %s → %s.\n\n", appName, from, to)
	fmt.Fprintf(&b, "Причина: %s (показатель %.0f%%).\n\n", autoscaleReasonRU(reason), ratio*100)
	b.WriteString("Приложение перезапустилось с новыми лимитами — это короткий перерыв в работе, после которого оно должно отвечать быстрее.\n\n")
	b.WriteString("Тарификация не изменилась: счёт зависит от количества приложений, баз и доменов, а не от их размера.\n\n")
	fmt.Fprintf(&b, "Открыть приложение в консоли: %s\n\n", consoleLink)
	b.WriteString("Размер подбирается платформой, вручную указывать его не нужно. Увеличение происходит не чаще раза в 6 часов на приложение.\n")
	return subject, b.String()
}

// ComposeAutoscaleShrink reports a resize DOWN, which needs a different first
// paragraph than a resize up: the owner sees an unexplained restart of an app
// that was working fine, and the obvious wrong conclusion — "they are taking
// resources away because I am not paying enough" — has to be closed immediately.
// So the evidence comes first (a week of measured peaks), then the fact that the
// app can grow straight back, then the reason it matters to them: the surplus
// was holding project quota their other apps could not use.
func ComposeAutoscaleShrink(appName, from, to, detail string, consoleLink string) (subject, body string) {
	subject = fmt.Sprintf("Dada Cloud: приложению %s уменьшены ресурсы (%s → %s)", appName, from, to)
	var b strings.Builder
	fmt.Fprintf(&b, "Приложение %s всю последнюю неделю использовало заметно меньше, чем занимало, поэтому платформа вернула излишек: %s → %s.\n\n", appName, from, to)
	fmt.Fprintf(&b, "Замеры за неделю: %s. Новый размер оставляет как минимум двукратный запас над пиком.\n\n", detail)
	b.WriteString("Приложение перезапустилось с новыми лимитами. Если нагрузка вырастет, платформа увеличит размер обратно автоматически — ничего указывать не нужно.\n\n")
	b.WriteString("Тарификация не изменилась: счёт зависит от количества приложений, баз и доменов, а не от их размера. Освободившийся запас вернулся в квоту проекта, и его смогут занять другие ваши приложения.\n\n")
	fmt.Fprintf(&b, "Открыть приложение в консоли: %s\n", consoleLink)
	return subject, b.String()
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

// render assembles RFC-5322 headers + UTF-8 body.
func (n *Notifier) render(to, subject, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", n.from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", encodeHeader(subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	return b.String()
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
