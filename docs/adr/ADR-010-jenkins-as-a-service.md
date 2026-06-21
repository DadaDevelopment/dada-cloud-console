# ADR-010: Jenkins-as-a-Service — Unified Build Platform, Retire build-agent

## Status

Proposed — 2026-06-21

Related: [PRD-mobile-delivery](../prd/PRD-mobile-delivery.md)

## Context

DADA Cloud needs Android CI (connect repo → Gradle build → APK/AAB). It currently runs **two** build systems:

- **dada-cloud `build-agent`** (commit f96566b): GitHub App → `builds` queue → k8s Job (BuildKit + Nixpacks) → **Harbor** (digest-pinned) → `deployments` row → gitops-agent deploys. Web/container apps only.
- **Jenkins** (`jenkins-lib` + `jenkins-pipelines`): Groovy shared lib, k8s pod templates, Maven/Node/Go/Python profiles → **Nexus**. No Android, no Gradle, no APK/AAB.

Neither builds Android today. The PRD explicitly specifies "Jenkins + Gradle + Android SDK." Android is Gradle-native — Jenkins' world, not BuildKit's. Jenkins already has the k8s-pod-build machinery, credential store, multibranch git, and a shared Groovy library purpose-built for this.

## Decision

### 1. Jenkins becomes the single build engine; retire build-agent

All builds — web-container **and** Android — run on Jenkins. The BuildKit/Nixpacks `build-agent` is removed. dada-cloud becomes the **Jenkins control plane**: trigger, poll, stream logs, collect artifacts. Existing `builds`/`builds_logs` tables, WS hub, and frontend log viewer are reused — only the *producer* swaps.

### 2. Deploy handoff unchanged

Jenkins replaces only the *build* step. On success the same `deployments` row + `DeployImageVersion` op is written → **gitops-agent is untouched**.

### 3. Registry: Harbor → Nexus (consolidate)

Container images move to the **Nexus Docker registry** (digest-pinned). gitops-agent is repointed Harbor→Nexus; **Harbor is retired**. Nexus is the single registry: Docker images + Maven/jars + (raw repo) APK/AAB.

### 4. Big-bang migration (accepted risk)

Web-container build cutover, registry swap (Harbor→Nexus), and build-agent retirement happen **together**, not via strangler. There is no parallel-run rollback lane. This risk is explicitly owned by the product owner.

### 5. Pipeline model: centralized + auto-detect, no user Jenkinsfile

dada-cloud owns the pipeline in `jenkins-lib`. User repos contain no Jenkinsfile. dada-cloud triggers a parameterized job (`repo, branch, framework, buildType, env`). Framework auto-detected (Android = `gradlew` + `AndroidManifest.xml`; else web-container) reusing Nixpacks-era detection; manual override available.

### 6. Control contract: REST poll + progressiveText log bridge

Go Jenkins client: `buildWithParameters` → queue item → poll result/duration; console via `logText/progressiveText` (incremental offset) → existing `builds_logs`/WS/viewer. No Jenkins-side plugins. Jenkins result mapped to `builds` states.

### 7. Artifacts: Nexus raw repo, dada-cloud-proxied download

APK/AAB → Nexus raw repo. `build_artifacts` table (`build_id, type, nexus_url, size, version_code, sha256`). Download proxied through dada-cloud (enforces `builds:read` + org isolation).

### 8. Android pod profile + debug-signing (MVP)

Net-new k8s pod profile: Android SDK 35+, Gradle, JDK, Gradle cache mount. MVP debug-signs (auto debug keystore). V2 release signing uses a dada-cloud encrypted project secret injected as a Jenkins credential.

### 9. Builds run under a service account

Jenkins authenticates to dada-cloud as IAM service account `jenkins-ci` (role Developer, scopes `builds:write`/`deploy:write`). See [ADR-009](ADR-009-iam-ownership-split.md).

## Alternatives considered

| Decision | Options | Chosen | Why |
|----------|---------|--------|-----|
| Android engine | Jenkins / extend build-agent | **Jenkins** | PRD mandate; Gradle-native; Jenkins has pod+cred+multibranch machinery |
| Scope | Android-only / unify all builds | **unify, retire build-agent** | Owner's call: one control plane, one build system |
| Deploy path | rewrite / keep gitops-agent | **keep** | Only build step changes; deploy proven |
| Registry | keep Harbor / migrate to Nexus | **Nexus** | Owner's call: single registry for images + jars + APK/AAB |
| Migration | strangler / big-bang | **big-bang** | Owner's call (risk noted below) |
| Jenkinsfile | centralized+autodetect / user pick / generated file | **centralized + auto-detect** | Vercel UX; no repo mutation; reuse detection |
| Control | poll+progressiveText / webhooks / hybrid | **poll + progressiveText** | No plugins; reuse existing build UI |
| Artifacts | Nexus raw / S3 / Jenkins archive | **Nexus raw** | One store; survives build rotation; auth-gated |
| Signing | MVP debug / sign releases now | **MVP debug, V2 release** | Play distribution is V2; don't build keystore mgmt yet |
| Git provider | GitHub MVP / all 3 / URL+deploy-key | **GitHub MVP** | Reuse existing GitHub App; Bitbucket/GitLab V2 |

## Consequences

**Positive**
- One build system, one registry, one control plane.
- Reuses Jenkins' mature pod/cred/multibranch infra and existing dada-cloud build UI.
- Android fits Jenkins/Gradle naturally.
- Deploy path and frontend log viewer unchanged.

**Negative / risks**
- **Big-bang cutover risk (primary)**: simultaneous web-build migration + Harbor→Nexus + build-agent retirement = no rollback lane. Mitigation required before cutover: full dry-run of the web-container Jenkins pipeline against a real app, verified image in Nexus, verified gitops-agent deploy from Nexus, and a documented manual fallback (re-enable build-agent + Harbor temporarily) even if "unsupported."
- Net-new: Android pod image, Gradle step, Go Jenkins client, progressiveText bridge, Nexus raw artifact flow.
- gitops-agent repoint Harbor→Nexus must be validated end-to-end.
- Second orchestration concern in dada-cloud (Jenkins API) replacing the build-agent code.

## Validation plan (pre-cutover)

1. Web-container Jenkins pipeline builds a real app → image in Nexus (digest-pinned).
2. gitops-agent deploys that image from Nexus successfully.
3. Android pipeline produces APK + AAB → Nexus raw → proxied download.
4. progressiveText log bridge shows live logs in existing viewer.
5. Only then: delete build-agent, retire Harbor, flip all apps to Jenkins.
