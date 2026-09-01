---
id: 0490
status: closed
prio: P0
stream: 2
title: springPipeline CPS: MavenXpp3Reader NotSerializable ломает деплой всех spring-репо с 08-18
created: 2026-08-31
sess: sess-0831a
closed_at: 2026-08-31
closed_commit: 8b1e8bd
closed_note: jenkins-lib CPS фикс 8b1e8bd push, gateway build #4/#5 SUCCESS, образ -5 в nexus, infraProject=internal доведён 386440c, под gateway Ready/health 200. ЗАКРЫТ live
---
ПРОДОЛЖЕНИЕ 0488 (закрыт ошибочно в этом же цикле, переоткрыто как новый id).
Разбор: automator/tasks/2026-08-31-springpipeline-cps-pom-broken.md
[live Jenkins] spring.gateway/develop #3 (16:35Z) + event-service/feedback-service/user-service #1 (08-18):
Build && Test = SUCCESS, стадия "Docker Deploy && Deploy to ArgoCD" падает:
  Ошибка при чтении POM: java.io.NotSerializableException:
    org.apache.maven.model.io.xpp3.MavenXpp3Reader
  deployDockerImageToNexus.call(deployDockerImageToNexus.groovy:4)
  NPE: getVersion() on null
Блокирует доставку фикса gateway-крэшлупа (spring.gateway 0ab09d9, BUILD SUCCESS).
Фикс: dada-tuda-jenkins-pipelines@develop, deployDockerImageToNexus.groovy -
читать версию pom через @NonCPS String-метод (или readFile+regex), не хранить
Model/Reader между CPS-шагами; guard на null. Закрывает 4 репо сразу.
Verify-бар: spring.gateway/develop зелёный И образ develop-0.0.1-SNAPSHOT-<N+1>
в nexus И под gateway в internal-prod не рестартует 30+ минут.
