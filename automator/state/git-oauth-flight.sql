-- Смертность перелёта на GitHub (поток 2 / H02).
--
-- Инструментировано sess-0813d (2026-08-13): `StartGitAppInstall` пишется до
-- того, как юзер уходит на github.com, `FinishGitAppInstall` — когда он
-- вернулся. Корреляция по `metadata->>'install_nonce'`. Строки различаются по
-- `metadata->>'flow'`:
--   app_install    — установка GitHub App (gitrepos.go, GetGitInstallURL/GitInstallCallback)
--   user_authorize — user-authorization OAuth (git_oauth.go)
--   public_clone   — sess-0822i: анонимный `linkGitRepo` без installation_id и
--                     без токена (публичный репозиторий клонируется голым URL).
--                     Не уходит на github.com, поэтому Start и Finish пишутся
--                     ОДНИМ запросом (см. gitrepos.go: linkGitRepo, insert CTE
--                     `flight`), а не в двух хендлерах: Finish для успеха едет
--                     той же SQL-статьёй, что и INSERT в git_repos, так что
--                     закоммиченная запись git_repos физически не может
--                     остаться без Finish-строки.
--
-- ДО sess-0822i этот поток не писал НИ ОДНОЙ строки: `kkartov@yandex.ru`
-- подключил репозиторий 6 раз, 0 строк в flight-паре. Число «дверь теряет
-- X%» до этой правки было смертностью app_install+user_authorize, молча
-- выдаваемой за смертность всей двери — оба потока обычно самые редкие,
-- потому что большинство подключений — это как раз public_clone.
--
-- ВАЖНО: до выкатки в прод запросы вернут ноль строк для public_clone —
-- инструмент не воскрешает уже существующие `git_repos`, подключённые до
-- деплоя. Ноль здесь = «данных ещё нет» (или «ещё не задеплоено»), а НЕ
-- «утечки нет».
--
-- СТАТУС sess-0813f (2026-08-13): код НА ПРОДЕ (образ `09880130` == HEAD, [live
-- kubectl]), все 4 запроса дают 0 строк — за 30д до git-connect физически дошёл
-- 1 юзер. То есть замер упирается не в выкатку, а в трафик: рега самообслуживания
-- в Keycloak закрыта (`/registrations` = 404 [live]), остался только Yandex-IdP.
--
-- ГИГИЕНА: агентские зонды пишут те же строки. `GET /git/install-url` из
-- песочницы `agent-sandbox` (project 7a387969-e082-415c-8b61-1f53f7e18295)
-- создаёт `StartGitAppInstall` без пары — при замере исключать этот project_id
-- и сервис-аккаунт, иначе смертность перелёта будет завышена собственным зондом.

-- 1. Смертность перелёта за 30 дней: ушёл и не вернулся.
SELECT s.metadata->>'flow'                        AS flow,
       count(*)                                   AS started,
       count(f.id)                                AS finished,
       count(*) - count(f.id)                     AS lost,
       round(100.0 * (count(*) - count(f.id)) / nullif(count(*), 0), 1) AS lost_pct
  FROM audit_events s
  LEFT JOIN audit_events f
         ON f.action = 'FinishGitAppInstall'
        AND f.metadata->>'install_nonce' = s.metadata->>'install_nonce'
 WHERE s.action = 'StartGitAppInstall'
   AND s.created_at > now() - interval '30 days'
 GROUP BY 1
 ORDER BY 1;

-- 2. Чем именно кончались вернувшиеся: вердикт и причина смерти.
SELECT outcome,
       coalesce(metadata->>'reason', '-') AS reason,
       metadata->>'flow'                  AS flow,
       count(*)                           AS n,
       count(DISTINCT actor_id)           AS users
  FROM audit_events
 WHERE action = 'FinishGitAppInstall'
   AND created_at > now() - interval '30 days'
 GROUP BY 1, 2, 3
 ORDER BY n DESC;

-- 3. Поимённо: кто ушёл на GitHub и не вернулся (кандидаты в разбор пути).
SELECT u.email,
       s.created_at,
       s.project_id,
       s.metadata->>'flow' AS flow
  FROM audit_events s
  JOIN users u ON u.id = s.actor_id
 WHERE s.action = 'StartGitAppInstall'
   AND s.created_at > now() - interval '30 days'
   AND NOT EXISTS (SELECT 1 FROM audit_events f
                    WHERE f.action = 'FinishGitAppInstall'
                      AND f.metadata->>'install_nonce' = s.metadata->>'install_nonce')
 ORDER BY s.created_at DESC;

-- 4. Сквозная связка с намерением из ux_events: клик по «Подключить GitHub»
--    (ux_events) -> интент (StartGitAppInstall) -> вердикт. Разрыв на первом
--    стыке = клик не доехал до бэкенда, на втором = умер сам перелёт.
SELECT date_trunc('day', s.created_at) AS day,
       count(*)                        AS started,
       count(*) FILTER (WHERE EXISTS (
              SELECT 1 FROM audit_events f
               WHERE f.action = 'FinishGitAppInstall'
                 AND f.metadata->>'install_nonce' = s.metadata->>'install_nonce')) AS returned
  FROM audit_events s
 WHERE s.action = 'StartGitAppInstall'
   AND s.created_at > now() - interval '30 days'
 GROUP BY 1
 ORDER BY 1 DESC;

-- 5. Смертность двери ЦЕЛИКОМ: три потока сведены в один свод плюс общий
--    итог ("all"). Это то число, что можно класть в приоритизацию активации
--    вместо строки 1 по одному только app_install/user_authorize.
SELECT flow, started, finished, lost, lost_pct FROM (
  SELECT s.metadata->>'flow'                        AS flow,
         count(*)                                   AS started,
         count(f.id)                                AS finished,
         count(*) - count(f.id)                     AS lost,
         round(100.0 * (count(*) - count(f.id)) / nullif(count(*), 0), 1) AS lost_pct,
         1 AS sort_key
    FROM audit_events s
    LEFT JOIN audit_events f
           ON f.action = 'FinishGitAppInstall'
          AND f.metadata->>'install_nonce' = s.metadata->>'install_nonce'
   WHERE s.action = 'StartGitAppInstall'
     AND s.created_at > now() - interval '30 days'
   GROUP BY 1
  UNION ALL
  SELECT 'all' AS flow,
         count(*)                                   AS started,
         count(f.id)                                AS finished,
         count(*) - count(f.id)                     AS lost,
         round(100.0 * (count(*) - count(f.id)) / nullif(count(*), 0), 1) AS lost_pct,
         2 AS sort_key
    FROM audit_events s
    LEFT JOIN audit_events f
           ON f.action = 'FinishGitAppInstall'
          AND f.metadata->>'install_nonce' = s.metadata->>'install_nonce'
   WHERE s.action = 'StartGitAppInstall'
     AND s.created_at > now() - interval '30 days'
) x
 ORDER BY sort_key, flow;

-- 6. M2-проверка: число успешных подключений, которые эта метрика видит
--    (Finish outcome='success', любой flow), должно совпасть с числом строк
--    git_repos, реально созданных живыми пользователями (created_by NOT NULL,
--    актор — не системный) за то же окно. Историчиеские git_repos, связанные
--    до деплоя этой инструментации, этот инструмент не воскрешает — сверка
--    честна только "с момента отгрузки", отсюда общее окно на обеих сторонах.
SELECT
  (SELECT count(*) FROM audit_events
    WHERE action = 'FinishGitAppInstall' AND outcome = 'success'
      AND created_at > now() - interval '30 days') AS flight_success_rows,
  (SELECT count(*) FROM git_repos gr
     JOIN users u ON u.id = gr.created_by
    WHERE gr.created_at > now() - interval '30 days') AS git_repos_created_by_live_actor;
