# audit-path-graph (перезаписывается каждый цикл; последний разбор 2026-09-16 sess-0916a)

## Окно: новые юзеры за 14 дней [live psql cloud-console 2026-09-16]
9 регистраций. Активация (CreateApp success): 5/9 = 56%.
| email | рег | канал | audit-строк | аппов | последний день |
|---|---|---|---|---|---|
| freitorsk@yandex.ru | 09-12 | yandex | 114 | 1 | 09-12 |
| wow83168@gmail.com | 09-10 | password | 38 | 1 | 09-10 |
| staffybot@mail.ru | 09-10 | password | 44 | 0 | 09-10 |
| y4ndex.danila@yandex.ru | 09-09 | yandex | 9 | 0 | 09-09 |
| dada-tuda.ru1@buss.gq | 09-08 | password | 7 | 0 | 09-08 |
| masaybasay@yandex.ru | 09-07 | yandex | 33 | 1 | 09-07 |
| yzfy@sls.xx.kg | 09-05 | password | 56 | 2 | 09-07 |
| ivakinavv23@yandex.ru | 09-03 | password | 18 | 0 | 09-03 |
| wgck@sls.xx.kg | 09-03 | password | 78 | 3 | 09-10 |

Мёртвых сигнапов (0 строк везде) в окне НЕТ - каждый новичок сделал хоть что-то.
Самый слабый: `dada-tuda.ru1@buss.gq` (7 строк, дошёл до CreateProject и встал).

## Терминальное действие (последнее НЕ-навигационное перед тишиной), 30 дней
| исход | кто |
|---|---|
| DeployImageVersion success | freitorsk, m206rv159, masaybasay, wgck, lifecoachrussia, messiajit4, kof97zip - 7 человек. Успешный выкат = нормальный конец сессии, не леак. |
| **ResolveAutofix success** | **wow83168 (nodejs-argo, 09-10)** - агент открыл PR, юзер ушёл в ту же минуту. |
| BuildFinished failure | y4ndex.danila (smirad), saravananofficial13, tarotreaderhimu - сдались на упавшем билде. |
| VerifyDomainAuthorization failure | staffybot (bot.horizonmobile.ru) - ACME, ушёл на неудачной проверке. |
| TriggerAutofix failure | ivakinavv23 (jkjk) - автофикс упал сам. |
| DeleteApp | yzfy (web) - portless-upload, закрыт E133. |
| InstallSolution failure | kkartov. |
| CreateProject | dada-tuda.ru1@buss.gq - не дошёл до аппа вообще. |

## Вывод цикла: терминальное действие `ResolveAutofix success` - это разрыв на ВЫХОДЕ агента
`ResolveAutofix success` не должно быть терминальным действием в принципе: это момент, когда
платформа СДЕЛАЛА работу. Разбор пути двух юзеров показал, почему оно им стало:

- **freitorsk 09-12** (`chirping-kolyaska`): TriggerAutofix 10:47:18 -> ResolveAutofix
  `pr_url=.../pull/1` 10:49:47 -> TriggerBuild 10:53:19 (тот же коммит, failed) ->
  BuildAutoRetried 13:42 -> TriggerBuild 14:12:57 -> 6x ViewApp, 3x ViewBuildLogs ->
  **DeleteApp 15:01:58**. Через 30 минут завёл другой апп с нуля.
- **wow83168 09-10** (`nodejs-argo`): app.diagnose -> TriggerAutofix 14:44:53 ->
  ResolveAutofix `pr_url=.../pull/1` 14:47:17 -> тишина. Апп до сих пор http=503
  (строка not_ready в ops-панели 09-16).

Причина [code, до фикса]: `pr_url` читала ровно одна поверхность - `CrashPullRequests`
внутри баннера рантайм-краша (app-alerts-banner.tsx:568,820), которому нужен хотя бы раз
стартовавший контейнер. Оба юзера упали НА СБОРКЕ, контейнера не было, значит PR был
невидим на каждом экране, куда они заходили.

Отгружено `b6a1d7a1`: блок «исправление готово, ждёт в пул-реквесте» на карточке упавшего
билда (страница аппа) и на странице билда, оба с маркерами `autofix_pr_notice:<surface>`
view/click. Замер - E134, measure_after 2026-10-07.

## Уточнение инструментирования, заведённое этим же циклом
До сегодня переход «агент отдал PR -> юзер его открыл» был структурно неизмерим: клик по
ссылке PR никуда не писался (в баннере краша ссылка была без `data-ux`). Новый блок несёт
`autofix_pr_notice:<surface>` view + `:open_pr` click, поэтому конверсия показ->переход
теперь считается тем же способом, что и app_next_step / build_success_cta.

## Панель: not_ready_other = unmaintained инфра-хвосты (payments/ai-gateway/zerkalo - PublicApi без ingress, phase Unknown) - не юзерские, не трогать (решение повторено 09-10, владелец в курсе).
