---
id: 0313
status: open
prio: P0
stream: 6
title: P1-BOX-DCR-BLOCKED · **Анонимная динамическая регистрация OAuth-клиента (RFC 7591) у нас ЗАКРЫТА:** POST https://id.dada-tuda.ru/r
sess: sess-0801i
section: 📣 ПОТОК 6 — ПРОДАТЬ МЕХАНИКУ /box (owner-directive 2026-08-01 интерактив)
---
- [ ] 🔴 P1-BOX-DCR-BLOCKED (sess-0801i, [live] проверено curl'ом) · **Анонимная динамическая регистрация OAuth-клиента (RFC 7591) у нас ЗАКРЫТА:** `POST https://id.dada-tuda.ru/realms/master/clients-registrations/openid-connect` → `403 {"error":"insufficient_scope","error_description":"Policy 'Trusted Hosts' rejected request to client-registration service. Details: Host not trusted."}`. При этом `/.well-known/oauth-protected-resource` честно объявляет Keycloak как authorization server, а метаданные AS содержат `registration_endpoint` — значит MCP-клиент, который следует спеке и пробует DCR (это дефолт у Claude Code/VS Code для http-транспорта), доходит до регистрации и ОТВАЛИВАЕТСЯ. Сейчас это обойдено ЯВНЫМ прибитым client_id: плагин запускает `mcp-remote ... --static-oauth-client-info {"client_id":"dada-mcp"}` [origin `mcp-plugin/.claude-plugin/plugin.json`], и этот путь рабочий — клиент `dada-mcp` публичный и принимает ПРОИЗВОЛЬНЫЙ localhost-порт в redirect_uri (проверено на 3334/51823/60999 → HTTP 200, страница логина, без `Invalid parameter`), т.е. случайный порт mcp-remote не ломает вход. Решение по DCR = продуктовое, НЕ молчаливый тумблер: открыть анонимную регистрацию на master-реалме = позволить кому угодно плодить клиентов в нашем IdP. Пока не решено — ЛЮБАЯ инструкция подключения обязана нести `--static-oauth-client-info`, иначе мы публикуем сломанный конфиг.
