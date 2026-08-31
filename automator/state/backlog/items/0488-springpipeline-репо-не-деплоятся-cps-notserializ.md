---
id: 0488
status: open
prio: P1
stream: 2
title: springPipeline-репо не деплоятся: CPS NotSerializableException в deployDockerImageToNexus
created: 2026-08-31
sess: sess-0831a
---
[hypothesis: platform-truth] Разбор [live Jenkins build #3 spring.gateway/develop, 16:35Z + #1 event-service/feedback-service/user-service от 08-18].

Симптом: Build && Test = BUILD SUCCESS, стадия "Docker Deploy && Deploy to ArgoCD" падает:
  Читаем pom по пути pom.xml
  Ошибка при чтении POM: java.io.NotSerializableException:
    org.apache.maven.model.io.xpp3.MavenXpp3Reader
  deployDockerImageToNexus.call(deployDockerImageToNexus.groovy:4)
  springPipeline.call(springPipeline.groovy:78)
  NPE: Cannot invoke method getVersion() on null object

Охват: ВСЕ 4 springPipeline-репо с веткой develop падают одинаково с 08-18
(когда 3e7b893 включил стадию deploy для gateway). ai-gateway (другой пайплайн,
прямой docker push) деплоится нормально.

Фикс (jenkins-lib, dada-tuda-jenkins-pipelines@develop): не держать
MavenXpp3Reader/Model между CPS-шагами — читать версию через @NonCPS-метод
возвращающий String, либо readFile+regex; guard на version==null с понятным
сообщением. Та же семья, что List.subList() в dada-cloud Jenkinsfile (#1321).

Пока не починено: фикс gateway-крэшлупа (0ab09d9, буфер WebClient 2MB)
компилируется, но НЕ может доехать до прода — образ не собирается пайплайном.
Обход: ручная сборка/пуш образа + бамп тега в argo-infra values (или чинить
jenkins-lib первым — правильнее, закрывает 4 репо сразу).
Разбор: automator/tasks/2026-08-31-springpipeline-cps-pom-broken.md
