# Fix internal-prod gateway CrashLoop: raise WebClient buffer + mutable route list

## Root cause [live kubectl logs, 2026-08-31]
`internal-prod/gateway-deploy` (image develop-0.0.1-SNAPSHOT-27) crash-loops 709+ restarts
over 3d5h. Startup sequence:

1. `PublicApiCatalogService.loadPublicApis` does WebClient GET
   `/apis/platform.dada-tuda.ru/v1alpha1/publicapis` and buffers the whole body.
2. The PublicApi list is now > 256KiB ->
   `DataBufferLimitException: Exceeded limit on max bytes to buffer : 262144`.
3. Exception path still calls `.sort()` on `List.of(...)` immutable list ->
   `UnsupportedOperationException` (PublicApiCatalogService.refresh:107).
4. Exception propagates out of `refreshOnStartup` (ApplicationReadyEvent listener)
   -> `Application run failed` -> container exit -> CrashLoopBackOff.

The scheduledRefresh path logs WARN and survives; the STARTUP path kills the app.
So the gateway is down for good while the PublicApi list body stays over 256KiB.

## Why now
PublicApi CR count/type-size grew (user apps + platform PublicApis). No code
change needed - the CR list just crossed the default Spring WebFlux codec
limit of 262144 bytes. The immutable-list `.sort()` bug was always latent.

## Fix (product code, DADA gateway-service repo)
1. Raise the WebClient max-in-memory buffer, e.g.
   `spring.codec.max-in-memory-size: 2MB` (application.yaml) or
   `.exchangeStrategies(b -> b.codecs(c -> c.defaultCodecs().maxInMemorySize(2 * 1024 * 1024)))`.
2. Replace `List.of(...)` with `new ArrayList<>(...)` before `.sort(...)`.

## Belt-and-suspenders (argo-infra, cluster values)
`helm/project-defaults` / gateway values can pass
`javaToolOptionsAppend: -Dspring.codec.max-in-memory-size=2MB` so an old image
also stops dying before the product fix ships. Chart already supports
`javaToolOptionsAppend` (helm/common/templates/deployment.yaml:214).

## Note
Argo CD manages this app on the MGMT cluster (context e7b608, ns argocd-master,
app `gateway-prod`, sources: dada-argo@develop helm/spring + argo-infra@console-migration
values). Synced/Progressing (crashloop keeps it from going Healthy). The fix must
ship as a new gateway-service SNAPSHOT image + values tag bump, or via the
JAVA_TOOL_OPTIONS append as an immediate stopgap.
