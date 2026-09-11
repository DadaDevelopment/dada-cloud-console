---
id: 0500
status: open
prio: P2
stream: 2
hypothesis: H02
title: Бэкенд верификации домена не различает NXDOMAIN и wrong-value: классификация живёт только во фронте
created: 2026-09-11
---
dec39f68 классифицирует ошибку строкой на фронте (frontend/lib/domain-verify-backoff.ts classifyVerifyFailure) по тексту, который бэкенд положил в error_message (backend/internal/api/domains.go:962 - fmt.Sprintf("DNS lookup failed for %s: %v", host, err)).

Это работает, но парсинг чужого текста хрупкий: смена формулировки в Go молча сломает подсказку во фронте.

Правильное место - структурное поле рядом с error_message (например failure_kind: not_published|wrong_value|other), проставляемое в verifyAuthorization (domains.go:929-983), где реальный тип ошибки ещё известен (net.DNSError.IsNotFound против несовпадения значения TXT).

Не срочно: текущая классификация покрывает 100% живых случаев (все 145 неудач в аудите - один паттерн "no such host").
