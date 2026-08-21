---
id: 0369
status: closed
prio: P0
stream: 3
hypothesis: H08
title: ENOSPC-краш не получал вердикта: сигнатура матчилась с учётом регистра, Python печатает с заглавной
created: 2026-08-19
sess: sess-0819e
closed_at: 2026-08-19
closed_commit: 83af9559
closed_note: ENOSPC-сигнатура матчилась case-sensitive: strerror/CPython пишут "No space left on device" с большой N, Go — с маленькой. fonbet-value 5 дней/39 рестартов с cause_kind=NULL → баннер пустой, панель читала аварию как код юзера. containsFold применён к platformCrashSignatures на ОБОИХ местах (вердикт + строка-улика), языковые таблицы остались case-sensitive. RED показан выводом, GREEN + полный backend-сьют на реальной БД.
---
🔴 ЗАКРЫТО в этом же цикле (sess-0819e, 2026-08-19). Оставлено как запись причины.

**Симптом [live].** `fonbet-value` (artemmendeleev@gmail.com, наш самый ценный активный юзер)
крашлупил 5 суток, 39 рестартов, `cause_kind` = NULL всё это время. Баннер краша
(`frontend/components/deploy/app-alerts-banner.tsx:565`) рендерит блок ТОЛЬКО при непустом
`cause_kind`, поэтому владелец 5 дней смотрел в пустое место.

**Корень [code `backend/internal/notify/notify.go:442`].** Сигнатура ENOSPC лежала строкой
`"no space left on device"` и матчилась `strings.Contains` — с учётом регистра. Настоящий лог:
`OSError: [Errno 28] No space left on device: '/data/raw_data/...'` — заглавная `N`, потому что
`strerror(ENOSPC)` именно так и печатает, а CPython/Node отдают строку дословно. Go в своём
`os` тот же errno лоуеркейсит — отсюда и взялся лоуеркейс-паттерн.

**Почему тест не поймал.** `TestClassifyCrashCauseEnospc` подавал в классификатор
СВОЮ лоуеркейсную строку, а не ту форму, которая единственно встречается в живом контейнере.
Тест зелёный, продукт слепой.

**Фикс.** `containsFold` для `platformCrashSignatures` (и только для них: там ОС-строки, чей
регистр выбирает рантайм; в языковых таблицах регистр — часть имени типа исключения).
`ExtractCauseLine` матчит тем же правилом через `crashLinePattern.fold`, иначе вердикт и его
улика выбирались бы разными правилами (см. память
`project_verdict_and_its_evidence_line_were_chosen_by_different_rules`).
