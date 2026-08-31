# spring.gateway: deployDockerImageToNexus сломан во ВСЕХ ветках (не только develop)

## Симптом [live Jenkins build #3 spring.gateway/develop, 2026-08-31 16:35]
Build && Test = BUILD SUCCESS (компиляция и тесты проходят, образ НЕ собирается).
Падает стадия "Docker Deploy && Deploy to ArgoCD":

```
Читаем pom по пути pom.xml
Ошибка при чтении POM: java.io.NotSerializableException:
    org.apache.maven.model.io.xpp3.MavenXpp3Reader
deployDockerImageToNexus.call(deployDockerImageToNexus.groovy:4)
springPipeline.call(springPipeline.groovy:78)
java.lang.NullPointerException: Cannot invoke method getVersion() on null object
```

## Класс ошибки [code jenkins-lib]
CPS-несериализуемый объект в пайплайне: `MavenXpp3Reader` (и/или результат
`readModel`) держится в переменной между CPS-чанкaми -> Jenkins не может
сериализовать состояние -> stage падает, `getVersion()` дальше получает null.
Это та же семья, что и `List.subList()` в dada-cloud Jenkinsfile (#1321,
feedback-jenkins-cps-needs-serializable-collections).

## Охват
- spring.gateway/develop #3 FAILURE (наш фикс компилируется, но не деплоится)
- spring.event-service/develop #1 FAILURE (08-18)
- spring.feedback-service/develop #1 FAILURE (08-18)
- spring.user-service/develop #1 FAILURE (08-18)
- ai-gateway (другой пайплайн, docker push напрямую) — SUCCESS 08-31 06:14
Вывод: ВСЕ springPipeline-репо с `infra: true` не могли деплоить с 08-18
(когда 3e7b893 "ci: deploy via infra:true" включил эту стадию для gateway).

## Фикс (в jenkins-lib, dada-tuda-jenkins-pipelines@develop)
В `deployDockerImageToNexus.groovy` / `springPipeline.groovy`:
- не хранить MavenXpp3Reader/Model между шагами; читать версию за один
  `sh` с groovy-скриптом или `readFile` + regex по `<version>`, либо
  `@NonCPS`-метод, который возвращает String и сразу сериализуется;
- guard: если version == null — fail с понятным сообщением, а не NPE.

## Обход для gateway прямо сейчас
Вариант A (быстро): собрать образ руками на машине с docker/dind и запушить
  nexus.dada-tuda.ru/dada/gateway-service:develop-0.0.1-SNAPSHOT-<N+1>,
  поднять тег в argo-infra values.yaml (clusters/beget-prod/projects/internal/
  environments/prod/apps/gateway/values.yaml) — commit + Argo сам подкатит.
Вариант B: чинить jenkins-lib (правильный путь, закрывает 4+ репо).

Live-машинa цикла (hermes sandbox) не имеет docker/jdk, поэтому сборка образа
и правка jenkins-lib — следующий шаг; право push в dada-tuda-jenkins-pipelines
проверить отдельно.
