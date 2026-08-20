---
id: 0397
status: open
prio: P1
stream: reliability
title: Jenkins — одна реплика, Recreate, без PDB: любой его рестарт гасит все сборки
created: 2026-08-20
sess: sess-0820d
---
kubectl get deploy jenkins -n devops-tools: replicas=1, strategy=Recreate;
kubectl get pdb -n devops-tools — пусто. Любая правка конфига, бамп образа или drain ноды =
100% сборок в 503. Клиентский бэкофф (4 попытки, ~7с, build-agent/internal/jenkins/client.go:160-192)
закрывает моргание load-shed, а не рестарт контроллера, который длится минуты.
platform_error — 13 из 27 упавших сборок за 30 дней у 5 юзеров; 1 из 15 юзеров получил так
свою ПЕРВУЮ в жизни сборку.
