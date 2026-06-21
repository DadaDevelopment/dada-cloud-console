# PRD: DADA Cloud Mobile Delivery

## Status

Draft — 2026-06-21

Related: [ADR-010: Jenkins-as-a-Service Unified Build Platform](../adr/ADR-010-jenkins-as-a-service.md), [PRD-IAM](PRD-IAM.md)

## Summary

Connect a git repository, build an Android app, get downloadable APK/AAB artifacts with status / logs / duration. Under the hood: **Jenkins + Gradle + Android SDK**. As part of this work, dada-cloud's existing BuildKit/Nixpacks build-agent is **retired** and *all* builds (web-container + Android) move onto Jenkins, with dada-cloud acting as the Jenkins control plane.

## Goals (MVP)

- Connect a GitHub repository.
- Auto-detect Android vs web-container; build accordingly.
- Android build via Jenkins + Gradle + Android SDK.
- UI: status, logs, duration, artifacts.
- Artifacts: APK and AAB (debug-signed in MVP).

## Non-Goals (MVP → V2)

- Bitbucket / GitLab connection (V2; MVP is GitHub-first reusing the existing GitHub App).
- Release signing with a user keystore (V2).
- Google Play / Internal Testing / Beta / Production distribution (V2).

## Architecture

See [ADR-010](../adr/ADR-010-jenkins-as-a-service.md) for full rationale and migration risk.

### Build engine: Jenkins-as-a-Service (unified)

- Jenkins becomes the **single** build engine. The dada-cloud build-agent (k8s Job + BuildKit + Nixpacks) is removed.
- dada-cloud = **control plane**: triggers Jenkins jobs, polls status/duration, streams console logs, collects artifacts.
- Reuses existing `builds` / `builds_logs` tables, WS hub, and frontend build-log viewer — only the *producer* swaps (build-agent → Jenkins).

### Migration (big-bang — accepted risk)

- **gitops-agent stays**: Jenkins replaces only the *build* step. On success, the same `deployments` row + `DeployImageVersion` op is written → gitops-agent deploys unchanged.
- **Registry: Harbor → Nexus.** Container images now push to the Nexus Docker registry (digest-pinned). gitops-agent is repointed Harbor→Nexus; Harbor is retired. Nexus also holds Maven/jar artifacts and (raw repo) APK/AAB.
- **Big-bang cutover**: web-container builds, registry swap, and build-agent retirement happen together. Risk (no rollback lane) is recorded in ADR-010 and owned.

### Pipeline model: centralized + auto-detect (no user Jenkinsfile)

- dada-cloud owns the pipeline in `jenkins-lib`. User repos contain **no Jenkinsfile**.
- dada-cloud triggers a parameterized job (`repo, branch, framework, buildType, env`).
- Framework auto-detected (Android = `gradlew` + `AndroidManifest.xml`; else web-container), reusing the Nixpacks-era detection logic. Manual override dropdown when detection is wrong.

### Build agents

- Kubernetes pod templates (existing `kubePodTemplate.groovy`).
- **Net-new Android pod profile**: image with Android SDK 35+, Gradle, JDK; Gradle cache mount.

### Control contract: REST poll + progressiveText log bridge

- Go Jenkins client: `buildWithParameters` → queue item → poll build number / result / duration.
- Console streamed via Jenkins `logText/progressiveText` (incremental offset) → relayed into existing `builds_logs` + WS hub → existing frontend viewer (no UI rework).
- Jenkins result (`SUCCESS / FAILURE / ABORTED / building`) mapped to `builds` states. Poll ~2s while building.
- No Jenkins-side plugins required.

### Artifacts: Nexus raw repo

- Pipeline uploads APK/AAB to a Nexus **raw/hosted** repo.
- dada-cloud records `build_artifacts`: `build_id, type (apk|aab), nexus_url, size, version_code, sha256`.
- UI download is **dada-cloud-proxied** (enforces `builds:read` scope + org isolation), not a raw Nexus link. Survives Jenkins build rotation.

### Signing (MVP vs V2)

- **MVP**: debug-signed only (Gradle auto debug keystore) — proves the pipeline, produces APK/AAB.
- **V2**: release signing. Keystore + passwords stored as a **dada-cloud encrypted project secret** (reuse existing encrypted `env_vars` mechanism), injected into the Jenkins job as a credential at build time. Secret ownership stays in dada-cloud UI/RBAC.

### Git connection (MVP GitHub-first)

- Reuse the existing GitHub App (OAuth + webhook → trigger) from the web-build era. Same connect flow, different build type via auto-detect.
- Trigger = webhook push (existing) + manual "Build" button.
- Bitbucket / GitLab = V2 (Jenkins already has Bitbucket creds; GitLab is greenfield).

## Auth

- Builds run under a **service account** (IAM): `jenkins-ci`, role `Developer`, scoped key (`builds:write`, `deploy:write`). No human login. See [PRD-IAM](PRD-IAM.md).
- Artifact download gated by `builds:read`.

## UI

- "Connect repository" (GitHub) → repo appears under the project.
- Build list: status, duration, trigger (commit), branch.
- Build detail: live console log (existing viewer), result, artifacts (APK/AAB download).

## V2 scope

- Bitbucket + GitLab OAuth/webhooks.
- Release signing (keystore secret).
- Google Play distribution: Internal Testing, Beta, Production tracks.

## Success criteria

- Connect a GitHub Android repo → push → APK + AAB downloadable from the console, with live logs and duration.
- Web-container apps build on Jenkins and deploy via gitops-agent exactly as before (post-migration parity).
- build-agent / BuildKit / Harbor fully removed; Nexus is the only registry.
- Builds run under the `jenkins-ci` service account, no human credentials.
