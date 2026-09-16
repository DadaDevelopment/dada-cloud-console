# ops-handoff 2026-09-16T04:05Z

MAIN: green (bac888f3, Jenkins DADA-GH/dada-cloud-console/main #73 SUCCESS)
[live: Jenkins getBuild + probe-main-build.sh exit 0 (go build/vet backend,
build-agent, gitops-agent; go test gitops-agent OK; go test backend SKIP - нет
локального рига на 127.0.0.1:55432, см memory reference_local_realdb_test_rig;
frontend test:unit + lint OK). Диск был на грани (442 MiB своб., gate падал
PROBE-ENV-BROKEN) - снёс 3 dangling docker-образа (11.8G, ни один контейнер их
не использовал, проверено `docker ps -a --filter ancestor=<id>` = пусто) ->
2.9G своб., гейт прошёл чисто.]

DELIVERY: доставлено полностью (живой тег bac888f3 == origin/main, 0 строк
кода сверху) [live probe-delivery.sh]

PANEL: not_ready=2 (nodejs-argo, gulyaev-ai-core - юзерский код, оба http=503
на живых URL, владельцы wow83168@gmail.com и lifecoachrussia@yandex.ru),
other=3 (api-zerkalo-ru, ai-gateway-b72e67-dada-tuda-ru, payments - известные
хвосты unmaintained PublicApi), domains=3 (m2-delwedge-6ccb0a, a2a-hub.pro -
юзерские failed hostname/cert, известны с прошлых снимков; +НОВОЕ:
bot.horizonmobile.ru stage=authorization status=pending, юзер
staffybot@mail.ru - это ACME-челлендж в процессе, не failed, платформенных
признаков зависания нет), failed_builds=3 (smirad, jkjk, dadadev-brains -
юзерские), stuck_operations=0, blind=False [live pulse-remote.sh, снимок
возрастом 46 мин на момент прогона]
Панель практически идентична прошлому снимку - единственная новая строка
(bot.horizonmobile.ru pending) не поломка, а нормальная стадия выпуска
сертификата.

BLOCKED_USERS: none (stuck_operations=0, errors пусто; живые URL 33 проверено:
ok=21, мертвы=2 (оба юзерские 503 выше, уже известны), ответило_приложение=7,
никогда_не_отвечали=3 - без признаков блокировки платформой)

СДЕЛАНО: диск-инцидент - VM была на 442 MiB свободного места, гейт main не мог
собраться (PROBE-ENV-BROKEN). Нашёл и снёс 3 dangling docker-образа (11.8G,
незалинкованные ни на один контейнер) -> 2.9G свободно, гейт прогнан и зелёный
[live: df + docker rmi + повторный прогон probe-main-build.sh]. Больше
ничего чинить не пришлось - доставка полная, панель без наших причин.
Замечена не своя WIP-правка automator/state/backlog.md (lock-release от
параллельной Automator-сессии, sess-0915a истёк) - НЕ трогал, не мой файл
в этом цикле.

ПРОДУКТУ: без новых симптомов с прошлого снимка. Открыт 0498
(framework_undetected на upload-пути), незакрытые продуктовые 0492/0493
(валидация порта и домена), 0491 (инструментация форм), 0495
(recovery-бюджет билдов), 0503/0501 (archive_port/summary_json клобберинг),
0499 (upload-архив без манифеста -> живой URL), 0505 (PR автофикса невидим на
странице упавшего билда - lock снят другой сессией, ждёт продолжения). Диск
VM держать под наблюдением - за сутки ушли с ~1G до 442MB, это второй раз в
недавней истории; если участится, стоит завести отдельный
disk-guard/автоочистку dangling-образов в cron, а не разбирать руками
каждый прогон.
