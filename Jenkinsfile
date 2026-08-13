def GO_VERSION   = '1.25'
def NODE_VERSION = '22'

def GO_BUILDER_IMAGE   = "golang:${GO_VERSION}-alpine"
def NODE_BUILDER_IMAGE = "node:${NODE_VERSION}-bookworm"
// System deps + browsers preinstalled — kills the per-build ~90s
// `playwright install --with-deps` (apt + browser download) in the e2e stages.
// Tag MUST stay in lockstep with @playwright/test in frontend/package-lock.json;
// on drift the in-stage `npx playwright install chromium` downloads the matching
// browser (no apt) as a slow-but-green fallback until this tag is bumped.
def PLAYWRIGHT_IMAGE   = 'mcr.microsoft.com/playwright:v1.61.1-noble'
// docker:24, not docker:29 — docker:29-dind's embedded BuildKit "session"
// healthcheck fatally kills dockerd on heavy builds ("only one connection
// allowed" → dind SIGTERM → pod OOM/abort). docker:24 is the in-repo-proven-good
// dind (see jenkins-pipelines kubePodTemplate commit 01d47ea).
def DOCKER_CLI_IMAGE   = 'docker:24-cli'
def DOCKER_DIND_IMAGE  = 'docker:24-dind'

def GITHUB_REGISTRY      = 'ghcr.io'
def GITHUB_ORG           = 'dadadevelopment'
def BACKEND_IMAGE         = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-backend"
def FRONTEND_IMAGE        = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-frontend"
def GITOPS_AGENT_IMAGE    = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-gitops-agent"
def PORTAINER_AGENT_IMAGE = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-portainer-agent"
def BUILD_AGENT_IMAGE     = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-build-agent"
def GATEWAY_IMAGE         = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-gateway"
def EMBED_GATEWAY_IMAGE   = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-grafana-embed-gateway"
// The body a box runs as (ADR-019). Not one of the console components: it is not
// deployed, it is PULLED by box pods in the dada-boxes namespace, and it is pinned
// by the boxcatalog entry rather than by the ArgoCD write-back below.
def BOX_WARM_IMAGE        = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-box-warm"

def PUSH_WITH_RETRY_SH = '''
                                push_with_retry() {
                                    ref="$1"
                                    attempt=1
                                    delay=30
                                    while : ; do
                                        if docker push "$ref"; then
                                            return 0
                                        fi
                                        if [ "$attempt" -ge 5 ]; then
                                            echo "DADA_PUSH_GAVE_UP ref=$ref attempts=$attempt"
                                            return 1
                                        fi
                                        echo "DADA_PUSH_RETRY ref=$ref attempt=$attempt sleep=${delay}s"
                                        sleep "$delay"
                                        attempt=$(( attempt + 1 ))
                                        delay=$(( delay * 2 ))
                                    done
                                }
'''

// GitOps write-back: after a successful push, pin the just-built tag into the
// ArgoCD source so prod actually rolls (there is NO image-updater; the tag is
// the deploy trigger). Argo's prod app tracks the console-migration branch of
// argo-infra; the 6 console component tags (backend, frontend, gateway,
// gitopsAgent, portainerAgent, buildAgent) are pinned in lockstep (migrationJob
// is left alone). Gateway MUST stay in this list: it carries internal/telemetry,
// so omitting it strands the OTLP ingest path on a stale image while reads update.
def ARGO_REPO        = 'github.com/DadaDevelopment/argo-infra.git'
def ARGO_BRANCH      = 'console-migration'
def ARGO_VALUES_PATH = 'clusters/beget-prod/projects/platform/environments/prod/apps/cloud-console/values.yaml'

// Frontend OIDC build-time constants. NEXT_PUBLIC_* vars are baked into the
// JS bundle — must be set during both `npm run build` and `docker build`.
def NEXT_PUBLIC_AUTH_MODE       = 'oidc'
def NEXT_PUBLIC_KEYCLOAK_ISSUER = 'https://id.dada-tuda.ru/realms/master'
def NEXT_PUBLIC_OIDC_CLIENT_ID  = 'dada-console'
// Marketing landing (cloud.dada-tuda.ru) auth/console links point at the console
// host so OIDC login uses the whitelisted redirect URI. The same image serves
// both hosts — on the console host this is simply same-origin.
def NEXT_PUBLIC_CONSOLE_URL     = 'https://console.dada-tuda.ru'

def podLabel  = 'dada-cloud-console-agent'
def agentName = "kubeagent-${env.JOB_BASE_NAME}-${env.BUILD_NUMBER}-${UUID.randomUUID().toString().take(6)}"

// disableConcurrentBuilds WITHOUT abortPrevious: queue concurrent main pushes
// behind the running build instead of aborting it. abortPrevious starved the
// deploy — when main is pushed more often than a build takes (~11 min), every
// build was superseded (NOT_BUILT) before its GitOps write-back ran, so nothing
// ever deployed. Queueing lets each build finish + write back its tag in order.
//
// It regressed to abortPrevious: true and starved the deploy again: builds #813
// through #818 covered six pushed commits and four of them died as ERROR "This
// commit cannot be built" — superseded before the GitOps write-back — so prod sat
// on the tag of #812 while six commits' worth of work was already on main. The
// flag now matches the paragraph above it, and this note exists so the next person
// who reaches for abortPrevious sees what it costs here.
properties([
        disableConcurrentBuilds(),
        parameters([
                booleanParam(
                        name: 'RUN_E2E_AUTHED',
                        defaultValue: false,
                        description: 'Run the authenticated + mutating Playwright e2e (provisions a real DB) against the disposable e2e project. Needs the e2e-console-user + e2e-project-id credentials.'
                ),
                booleanParam(
                        name: 'BUILD_BOX_IMAGE',
                        defaultValue: false,
                        description: 'Also build and push the Dada Box warm image (backend/Dockerfile.box-warm), retagging it :v1. OFF by default since build #730 published the first :v1 — it is a multi-GB Ubuntu image that changes far less often than the console, and rebuilding it on every console commit costs six minutes of pipeline for a layer set nothing asked to change. Turn it ON for any commit that touches Dockerfile.box-warm or the toolchain a box is expected to ship with; nothing else republishes the tag every box pod pulls.'
                )
        ])
])

podTemplate(
        cloud: 'self-managed',
        label: podLabel,
        namespace: 'devops-tools',
        serviceAccount: 'jenkins-admin',
        yaml: """
apiVersion: v1
kind: Pod
metadata:
  name: ${agentName}
  annotations:
    # ArgoCD (app jenkins-beget, syncPolicy.automated.prune+selfHeal=true) prunes
    # this ephemeral agent pod on its ~180s reconcile tick — every build pod was
    # deleted mid-run at the :42s reconcile phase (#146/#147 "Pod just failed",
    # ChannelClosedException), regardless of build stage. Dropping the
    # tracking-id alone did NOT stop it (ArgoCD still prunes extraneous resources
    # in the managed namespace). These opt-outs tell ArgoCD to ignore the pod and
    # never prune it.
    argocd.argoproj.io/compare-options: IgnoreExtraneous
    argocd.argoproj.io/sync-options: Prune=false
  labels:
    app.kubernetes.io/name: jenkins-agent
    app.kubernetes.io/part-of: dada-cloud-console
    app.kubernetes.io/managed-by: jenkins
spec:
  # Override the namespace default activeDeadlineSeconds. devops-tools caps pod
  # lifetime at ~360s: EVERY agent pod was killed exactly ~360s after creation
  # (#146 pods 359s/360s apart; #148 pod 362s), which looked like a periodic
  # external pruner but is a per-pod deadline. The build needs longer than 6 min
  # (tests + frontend build + 4 image builds + push), so it never finished.
  # 1 hour is plenty and well under any sane runaway cap.
  activeDeadlineSeconds: 3600
  priorityClassName: ci-agent
  # Never schedule onto a node that carries a platform postgres replica. The
  # agent's docker-graph-storage + workspace are emptyDir, i.e. the NODE's disk,
  # and a build holds ~9.6 GiB of it. Twice this combination ended as a P0: the
  # node filled up and the platform postgres on it died (once taking a live
  # user's app down for 5h11m). Build #1083 then died itself on ENOSPC inside
  # `npm ci` while zh58h — which hosts pg-shard-0-postgresql-0 — sat at 99.8%.
  # Scoped to the databases namespace so a user pod that happens to be labelled
  # postgresql does not fence the agent out of a node. Today this leaves two
  # eligible nodes; if a future postgres replica lands on both, agents go
  # Pending (visible) instead of silently starving a database (not).
  affinity:
    podAntiAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        - topologyKey: kubernetes.io/hostname
          namespaces: ["databases"]
          labelSelector:
            matchExpressions:
              - key: app.kubernetes.io/name
                operator: In
                values: ["postgresql"]
  securityContext:
    fsGroup: 1000
  volumes:
    - name: workspace-volume
      emptyDir:
        sizeLimit: 3Gi
    - name: build-cache
      persistentVolumeClaim:
        claimName: jenkins-build-cache
    - name: docker-graph-storage
      emptyDir:
        sizeLimit: 8Gi
    - name: docker-certs
      emptyDir: {}
    - name: tools-volume
      emptyDir: {}
  containers:
    - name: jnlp
      # Digest-pinned (was floating :latest). A floating tag silently changes the
      # remoting version, which mid-build mismatches the 2.559-jdk21 controller and
      # drops the JNLP4 channel (ChannelClosedException, "removed or offline" —
      # observed #138/#139). This exact image handshakes cleanly against the
      # controller. Bump deliberately. Matches jenkins-pipelines kubePodTemplate.
      image: jenkins/inbound-agent@sha256:65b16b388a32be41d95134a5be7ce58e20fbc4d862e12f021ae64bf83bd5fe7d
      tty: true
      workingDir: /home/jenkins/agent
      resources:
        requests:
          # cpu 500m (was 50m): the JNLP4 channel drops deterministically during
          # the CPU-heavy 2nd-image go build (#143/#144: gitops-agent build ->
          # ChannelClosedException -> pod fails). go-builder+dind peg the node;
          # at a 300m cap the agent is starved and misses its heartbeat, so the
          # controller declares it offline. Reserving real CPU keeps the agent
          # responsive enough to heartbeat through the build.
          cpu: "500m"
          memory: "256Mi"
        limits:
          cpu: "2000m"
          memory: "512Mi"
      volumeMounts:
        - name: workspace-volume
          mountPath: /home/jenkins/agent
        - name: tools-volume
          mountPath: /tools

    - name: go-builder
      image: ${GO_BUILDER_IMAGE}
      command: ['cat']
      tty: true
      workingDir: /home/jenkins/agent
      env:
        - name: HOME
          value: /tmp
        - name: GOPATH
          value: /tmp/go
        - name: GOCACHE
          value: /tmp/.cache/go-build
        - name: GOMODCACHE
          value: /tmp/go/pkg/mod
        - name: CGO_ENABLED
          value: "0"
      resources:
        requests:
          cpu: "100m"
          memory: "256Mi"
        limits:
          cpu: "1500m"
          memory: "1536Mi"
      volumeMounts:
        - name: workspace-volume
          mountPath: /home/jenkins/agent
        - name: build-cache
          mountPath: /tmp/.cache/go-build
          subPath: go/build
        - name: build-cache
          mountPath: /tmp/go/pkg/mod
          subPath: go/mod
        - name: tools-volume
          mountPath: /tools

    # Postgres for the DB-backed backend tests.
    #
    # Without this container TEST_DATABASE_URL is unset in CI, so every test that
    # calls a pool helper (advisory locks, quota gates, storage caps) hits its
    # t.Skip and the "Backend tests" stage passes while testing none of them. The
    # gate was decoration. TestCIRequiresDatabase in backend/internal/api now fails
    # if this ever silently goes away again.
    #
    # trust auth and the image's default data dir on the container's own writable
    # layer: this database exists for the length of one build, holds no real data,
    # and is never reachable outside the pod. (Deliberately not /dev/shm — k8s
    # defaults that to 64Mi, which a fresh cluster plus WAL overruns.)
    - name: postgres
      image: postgres:16-alpine
      env:
        - name: POSTGRES_USER
          value: dada
        - name: POSTGRES_PASSWORD
          value: dada
        - name: POSTGRES_DB
          value: dada_test
        - name: POSTGRES_HOST_AUTH_METHOD
          value: trust
      resources:
        requests:
          cpu: "100m"
          memory: "256Mi"
        limits:
          cpu: "1000m"
          memory: "512Mi"
      readinessProbe:
        exec:
          command: ["pg_isready", "-U", "dada", "-d", "dada_test"]
        initialDelaySeconds: 2
        periodSeconds: 2
        failureThreshold: 30

    - name: node-builder
      image: ${NODE_BUILDER_IMAGE}
      command: ['cat']
      tty: true
      workingDir: /home/jenkins/agent
      env:
        - name: HOME
          value: /tmp
        - name: NPM_CONFIG_CACHE
          value: /tmp/.cache/npm
        - name: NEXT_TELEMETRY_DISABLED
          value: "1"
        - name: NODE_OPTIONS
          value: "--max-old-space-size=1536"
      resources:
        requests:
          cpu: "100m"
          # request == limit: next build spikes to ~2Gi. With request 256Mi the
          # container sits far over request during the build and is the kubelet's
          # first eviction victim under node MemoryPressure (pod evicted, dind
          # SIGTERM-exit-0, others 137 — builds #136/#139). Reserving the full 2Gi
          # protects it from node-pressure eviction.
          memory: "2Gi"
        limits:
          cpu: "1500m"
          memory: "2Gi"
      volumeMounts:
        - name: workspace-volume
          mountPath: /home/jenkins/agent
        - name: build-cache
          mountPath: /tmp/.cache/npm
          subPath: npm

    - name: playwright
      image: ${PLAYWRIGHT_IMAGE}
      command: ['cat']
      tty: true
      workingDir: /home/jenkins/agent
      env:
        - name: HOME
          value: /tmp
      resources:
        requests:
          cpu: "50m"
          memory: "256Mi"
        limits:
          cpu: "1000m"
          memory: "1536Mi"
      volumeMounts:
        - name: workspace-volume
          mountPath: /home/jenkins/agent

    - name: docker
      image: ${DOCKER_CLI_IMAGE}
      command: ['sh', '-c', 'cat']
      tty: true
      workingDir: /home/jenkins/agent
      env:
        - name: HOME
          value: /tmp
        - name: DOCKER_HOST
          value: tcp://localhost:2375
        - name: DOCKER_TLS_CERTDIR
          value: ""
      resources:
        requests:
          cpu: "50m"
          memory: "64Mi"
        limits:
          cpu: "250m"
          memory: "128Mi"
      volumeMounts:
        - name: workspace-volume
          mountPath: /home/jenkins/agent
        - name: tools-volume
          mountPath: /tools

    - name: dind
      image: ${DOCKER_DIND_IMAGE}
      securityContext:
        privileged: true
      env:
        - name: DOCKER_TLS_CERTDIR
          value: ""
      args:
        - --host=tcp://0.0.0.0:2375
        - --host=unix:///var/run/docker.sock
      resources:
        requests:
          cpu: "250m"
          # request == limit (1536Mi): dind burns memory building/exporting the
          # 4 images. With request 512Mi it runs over request and is evicted
          # under node MemoryPressure (#138/#141 die in the docker stage).
          # 1536Mi reserved protects it; 4Gi over-commits the node (caused the
          # next-build eviction in #136). Root crash was docker:29, now pinned 24.
          memory: "1536Mi"
          # ephemeral-storage: every build dies the moment dind extracts the 2nd
          # image's base layers (#143: golang:1.25-alpine extract -> channel drop).
          # The docker-graph-storage emptyDir counts as pod ephemeral storage; with
          # NO request, any usage is "over request" => this pod is the kubelet's #1
          # DiskPressure eviction victim (dind SIGTERM exit-0, others 137). Reserve
          # it so the build survives layer extraction.
          ephemeral-storage: "4Gi"
        limits:
          cpu: "1500m"
          memory: "1536Mi"
          ephemeral-storage: "8Gi"
      volumeMounts:
        - name: docker-graph-storage
          mountPath: /var/lib/docker
        - name: docker-certs
          mountPath: /certs
        - name: workspace-volume
          mountPath: /home/jenkins/agent
"""
) {
    // SURVIVE managed-control-plane / node-pressure flaps: the in-cluster JNLP4
    // channel drops mid-build (ClosedChannelException, pod "removed or offline")
    // when the Beget-managed apiserver flaps or the node evicts under memory
    // pressure (observed builds #136/#138). Retry ONLY on agent/channel loss —
    // a fresh pod is provisioned and the body reruns. Real failures
    // (compile/test/push) don't match agent() and still fail fast. Mirrors
    // jenkins-pipelines kubePodTemplate.
    retry(count: 5, conditions: [agent()]) {
    node(podLabel) {
        cleanWs()

        def commitAuthor  = ''
        def commitMessage = ''
        def resolvedTag   = ''
        def currentStageName = 'bootstrap'

        def runStage = { String name, Closure body ->
            currentStageName = name
            stage(name) { body() }
        }

        try {
            runStage('Checkout') {
                checkout scm
                commitAuthor  = sh(script: "git log -1 --pretty=format:'%an'", returnStdout: true).trim()
                commitMessage = sh(script: "git log -1 --pretty=format:'%s'", returnStdout: true).trim()
                def sha       = sh(script: 'git rev-parse --short=8 HEAD', returnStdout: true).trim()
                def tagOnHead = sh(script: 'git tag --points-at HEAD', returnStdout: true).trim()
                resolvedTag   = tagOnHead ?: sha
                env.RESOLVED_TAG = resolvedTag
                echo "Image tag: ${resolvedTag}  (commit: ${sha})"
            }

            // ── Build lanes: one pod, two containers, run concurrently ────
            // The Go backend lane and the Node frontend lane share no inputs,
            // so run them in parallel INSIDE THE SAME POD (go-builder vs
            // node-builder containers) instead of back-to-back. failFast aborts
            // the surviving lane the moment the other fails. Go sub-steps stay
            // sequential on purpose — fanning them out too would peg the node
            // CPU and drop the JNLP channel (see the resource notes above).
            // stage() (not runStage) inside the branches: currentStageName is
            // shared mutable state, unsafe to write from parallel branches.
            parallel(
                failFast: true,
                'backend (go)': {
                    container('go-builder') {
                        // The install is retried and its output is kept, because the
                        // alternative was tried and it lied: `apk add ... >/dev/null 2>&1
                        // || true` turned a transient fetch failure into `helm: not found`
                        // + exit 127 three lines later, with the actual apk error thrown
                        // away. Builds #1057 and #1059 died that way inside 25 minutes on
                        // 2026-08-11 while #1056 and #1058 passed on the identical image,
                        // and the OOM fix waiting behind them could not reach prod. The
                        // package is present and the repo is right (`golang:1.25-alpine`
                        // ships `community`, helm 3.19.0-r7 installs cleanly on demand) —
                        // what fails is reaching the mirror, so reaching it twice more is
                        // the fix that matches the cause. A swallowed failure must never
                        // again be diagnosed from the corpse two commands downstream.
                        stage('Toolchain (Go)') {
                            sh '''
                                set -eux
                                go version
                                for attempt in 1 2 3; do
                                    if apk add --no-cache helm git; then
                                        break
                                    fi
                                    if [ "$attempt" = 3 ]; then
                                        echo "apk add helm git failed three times; the alpine mirror is unreachable from this agent"
                                        exit 1
                                    fi
                                    echo "apk add failed (attempt ${attempt}/3), retrying in $((attempt * 5))s"
                                    sleep $((attempt * 5))
                                done
                                helm version --short
                            '''
                        }

                        stage('Cache hygiene') {
                            sh '''
                                set -eu
                                used=$(df -P /tmp/.cache/go-build | awk 'NR==2 {print $5}' | tr -d '%')
                                echo "shared build cache is ${used}% full"
                                if [ "$used" -ge 80 ]; then
                                    echo "over the 80% mark, dropping the Go build cache before it fills the volume"
                                    echo "it is regenerable; a full jenkins-build-cache PVC fails every build with ENOSPC (seen 08-02, builds 814-818)"
                                    go clean -cache
                                    df -h /tmp/.cache/go-build
                                fi
                            '''
                        }

                        stage('Go format check') {
                            sh '''
                                set -eu
                                unformatted=$(gofmt -l backend build-agent gitops-agent mcp-server portainer-agent tools/dbmove)
                                if [ -n "$unformatted" ]; then
                                    echo "gofmt violations in:"
                                    echo "$unformatted"
                                    echo "Fix locally: gofmt -w <file>"
                                    exit 1
                                fi
                                echo "all Go modules gofmt-clean"
                            '''
                        }

                        stage('Backend tests') {
                            dir('backend') {
                                // TEST_DATABASE_URL points at the postgres sidecar in the pod
                                // template. Before it existed, every DB-backed test in
                                // internal/api hit its t.Skip and this stage went green while
                                // testing none of them; TestCIRequiresDatabase now fails if that
                                // regresses.
                                //
                                // cmd/migrate retries the connection itself, so there is no
                                // sleep guess here and the build image needs no psql: Jenkins
                                // does not wait for sidecar readiness before running steps.
                                withEnv(['TEST_DATABASE_URL=postgres://dada:dada@localhost:5432/dada_test?sslmode=disable']) {
                                    sh 'go run ./cmd/migrate'
                                    sh 'go test ./... -count=1'
                                }
                            }
                        }

                        stage('Backend build') {
                            dir('backend') {
                                sh 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags="-s -w" -o bin/server ./cmd/server'
                            }
                        }

                        stage('Gateway build') {
                            dir('backend') {
                                sh 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags="-s -w" -o bin/gateway ./cmd/gateway'
                            }
                        }

                        // Built on EVERY build, not only when the box image is
                        // published: these two binaries are the box's init and its
                        // door, and a compile break in them must fail the build that
                        // introduced it rather than the next image build, weeks later.
                        stage('Box binaries build') {
                            dir('backend') {
                                sh 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags="-s -w" -o bin/box-init ./cmd/box-init'
                                sh 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags="-s -w" -o bin/box-broker ./cmd/box-broker'
                            }
                        }

                        stage('Grafana-embed-gateway build') {
                            dir('backend') {
                                sh 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags="-s -w" -o bin/grafana-embed-gateway ./cmd/grafana-embed-gateway'
                            }
                        }

                        stage('GitOps-agent tests') {
                            dir('gitops-agent') {
                                sh 'go test ./... -count=1'
                            }
                        }

                        stage('GitOps-agent build') {
                            dir('gitops-agent') {
                                sh 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags="-s -w" -o bin/gitops-agent ./cmd/gitops-agent'
                            }
                        }

                        stage('Build-agent tests') {
                            dir('build-agent') {
                                sh 'go test ./... -count=1'
                            }
                        }

                        stage('Build-agent build') {
                            dir('build-agent') {
                                sh 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags="-s -w" -o bin/build-agent ./cmd/build-agent'
                            }
                        }

                        stage('Portainer-agent tests') {
                            dir('portainer-agent') {
                                sh 'go test ./... -count=1'
                            }
                        }

                        stage('Portainer-agent build') {
                            dir('portainer-agent') {
                                sh 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags="-s -w" -o bin/portainer-agent ./cmd/portainer-agent'
                            }
                        }

                        stage('Helm lint + render') {
                            sh """
                                set -eux
                                helm lint helm/dada-cloud-console
                                helm template dada-cloud-console helm/dada-cloud-console \
                                  --namespace devops-tools \
                                  --set backend.image.tag=${resolvedTag} \
                                  --set frontend.image.tag=${resolvedTag} \
                                  --set gitopsAgent.image.tag=${resolvedTag} \
                                  --set portainerAgent.image.tag=${resolvedTag} \
                                  --set buildAgent.image.tag=${resolvedTag} \
                                  --set gateway.image.tag=${resolvedTag} \
                                  --set ingress.host=console.dada-tuda.ru \
                                  > /tmp/dada-cloud-console-rendered.yaml
                                echo "Rendered \$(wc -l < /tmp/dada-cloud-console-rendered.yaml) lines"
                            """
                        }
                    }
                },
                'frontend (node)': {
                    container('node-builder') {
                        stage('Frontend install') {
                            dir('frontend') {
                                // @dada/* deps resolve from internal Nexus npm (.npmrc).
                                // NEXUS_NPM_AUTH = base64("user:pass"); .npmrc expands it.
                                withCredentials([usernamePassword(
                                        credentialsId: 'docker-nexus-admin-psws',
                                        usernameVariable: 'NEXUS_USER',
                                        passwordVariable: 'NEXUS_PASS'
                                )]) {
                                    sh '''
                                        set -eu
                                        export NEXUS_NPM_AUTH=$(printf '%s:%s' "$NEXUS_USER" "$NEXUS_PASS" | base64 | tr -d '\\n')
                                        npm ci
                                    '''
                                }
                            }
                        }

                        stage('Frontend typecheck + tests + build') {
                            dir('frontend') {
                                withEnv([
                                    "NEXT_PUBLIC_AUTH_MODE=${NEXT_PUBLIC_AUTH_MODE}",
                                    "NEXT_PUBLIC_KEYCLOAK_ISSUER=${NEXT_PUBLIC_KEYCLOAK_ISSUER}",
                                    "NEXT_PUBLIC_OIDC_CLIENT_ID=${NEXT_PUBLIC_OIDC_CLIENT_ID}",
                                    "NEXT_PUBLIC_CONSOLE_URL=${NEXT_PUBLIC_CONSOLE_URL}",
                                ]) {
                                    // The `probe && run || echo skip` form this replaced looked like
                                    // "tolerate a missing script" but actually swallowed a FAILING
                                    // one: `||` binds to the whole `probe && npm run lint` chain, so
                                    // a real lint error exited 0 and printed "No lint script — skip".
                                    // Verified by reproducing it — a genuine
                                    // react-hooks/set-state-in-effect error passed this stage.
                                    // if/else keeps the missing-script tolerance and lets a real
                                    // failure fail the build, which is the whole point of the stage.
                                    sh '''
                                        set -eux
                                        has_script() {
                                            node -e "const p=require('./package.json'); process.exit(p.scripts && p.scripts['$1'] ? 0 : 1)"
                                        }
                                        if has_script typecheck; then npm run typecheck; else echo "No typecheck script — skip"; fi
                                        if has_script lint;      then npm run lint;      else echo "No lint script — skip";      fi
                                        if has_script test:unit; then npm run test:unit; else echo "No test:unit script — skip"; fi
                                        npm run build
                                    '''
                                }
                            }
                        }
                    }
                }
            )

            // ── Docker ────────────────────────────────────────────────────
            //
            // The image builds write into dind's /var/lib/docker, which is the
            // docker-graph-storage emptyDir — i.e. the NODE's filesystem, shared
            // with every other pod on that node. When the node runs out of space
            // the failure surfaces deep inside a build step as an unrelated-looking
            // tool error: #1083 (51225f09) died on `npm warn tar TAR_ENTRY_ERROR
            // ENOSPC` inside `npm ci` in the frontend image, 25 minutes in, and
            // read as "the commit broke the frontend" while the real cause was a
            // node at 95% disk. Prod then sat one commit behind main for hours.
            // Check the graph filesystem BEFORE burning the build, and name the
            // cause in the failure text so nobody re-diagnoses it from the corpse.
            container('dind') {
                runStage('Docker disk preflight') {
                    sh '''
                        set -eu
                        avail_kb=$(df -P /var/lib/docker | awk 'NR==2 {print $4}')
                        avail_g=$((avail_kb / 1024 / 1024))
                        df -h /var/lib/docker
                        if [ "$avail_g" -lt 5 ]; then
                            echo "CI NODE IS OUT OF DISK: ${avail_g}Gi free where the image builds need ~8Gi."
                            echo "This is OUR infrastructure, not this commit. Do not chase the build log."
                            echo "Free space on the node backing this agent pod, then rebuild."
                            exit 1
                        fi
                        if [ "$avail_g" -lt 10 ]; then
                            echo "WARNING: only ${avail_g}Gi free on the docker graph filesystem (~8Gi needed)."
                            echo "The node this agent landed on is close to full; the next build may ENOSPC."
                        fi
                    '''
                }
            }

            container('docker') {
                runStage('Docker build') {
                    sh """
                        set -eux
                        docker version
                        docker build \\
                          -t ${BACKEND_IMAGE}:${resolvedTag} \\
                          -f backend/Dockerfile backend
                        docker build \\
                          -t ${GATEWAY_IMAGE}:${resolvedTag} \\
                          -f backend/Dockerfile.gateway backend
                        docker build \\
                          -t ${EMBED_GATEWAY_IMAGE}:${resolvedTag} \\
                          -f backend/Dockerfile.grafana-embed-gateway backend
                        docker build \\
                          -t ${GITOPS_AGENT_IMAGE}:${resolvedTag} \\
                          -f gitops-agent/Dockerfile gitops-agent
                        docker build \\
                          -t ${PORTAINER_AGENT_IMAGE}:${resolvedTag} \\
                          -f portainer-agent/Dockerfile portainer-agent
                        docker build \\
                          -t ${BUILD_AGENT_IMAGE}:${resolvedTag} \\
                          -f build-agent/Dockerfile build-agent
                    """
                    if (params.BUILD_BOX_IMAGE) {
                        sh """
                            set -eux
                            docker build \\
                              -t ${BOX_WARM_IMAGE}:${resolvedTag} \\
                              -f backend/Dockerfile.box-warm backend
                        """
                    }
                    // Frontend image: npm ci inside the build pulls @dada/* from
                    // Nexus. Auth is passed as a BuildKit secret (never baked into
                    // a layer). NEXT_PUBLIC_* are build args (baked — they are public).
                    withCredentials([usernamePassword(
                            credentialsId: 'docker-nexus-admin-psws',
                            usernameVariable: 'NEXUS_USER',
                            passwordVariable: 'NEXUS_PASS'
                    )]) {
                        sh """
                            set -eu
                            printf '%s:%s' "\${NEXUS_USER}" "\${NEXUS_PASS}" | base64 | tr -d '\\n' > /tmp/nexus_npm_auth
                            DOCKER_BUILDKIT=1 docker build \\
                              --secret id=nexus_npm_auth,src=/tmp/nexus_npm_auth \\
                              --build-arg NEXT_PUBLIC_AUTH_MODE=${NEXT_PUBLIC_AUTH_MODE} \\
                              --build-arg NEXT_PUBLIC_KEYCLOAK_ISSUER=${NEXT_PUBLIC_KEYCLOAK_ISSUER} \\
                              --build-arg NEXT_PUBLIC_OIDC_CLIENT_ID=${NEXT_PUBLIC_OIDC_CLIENT_ID} \\
                              --build-arg NEXT_PUBLIC_CONSOLE_URL=${NEXT_PUBLIC_CONSOLE_URL} \\
                              -t ${FRONTEND_IMAGE}:${resolvedTag} \\
                              -f frontend/Dockerfile frontend
                            rm -f /tmp/nexus_npm_auth
                        """
                    }
                }

                // Push only on integration branches, not PRs
                def isPullRequest = (env.CHANGE_ID != null && env.CHANGE_ID != '')
                def shouldPush = !isPullRequest && (
                        env.BRANCH_NAME == 'main' ||
                        env.BRANCH_NAME == 'master' ||
                        env.BRANCH_NAME == 'develop'
                )

                if (shouldPush) {
                    runStage('Docker push') {
                        withCredentials([usernamePassword(
                                credentialsId: 'gh-token',
                                usernameVariable: 'GITHUB_USERNAME',
                                passwordVariable: 'GITHUB_TOKEN'
                        )]) {
                            sh """
                                set -eux
                                echo "\${GITHUB_TOKEN}" | docker login ${GITHUB_REGISTRY} -u \${GITHUB_USERNAME} --password-stdin
${PUSH_WITH_RETRY_SH}
                                push_with_retry ${BACKEND_IMAGE}:${resolvedTag}
                                push_with_retry ${FRONTEND_IMAGE}:${resolvedTag}
                                push_with_retry ${GITOPS_AGENT_IMAGE}:${resolvedTag}
                                push_with_retry ${PORTAINER_AGENT_IMAGE}:${resolvedTag}
                                push_with_retry ${BUILD_AGENT_IMAGE}:${resolvedTag}
                                push_with_retry ${GATEWAY_IMAGE}:${resolvedTag}
                                push_with_retry ${EMBED_GATEWAY_IMAGE}:${resolvedTag}
                                docker tag ${BACKEND_IMAGE}:${resolvedTag} ${BACKEND_IMAGE}:latest
                                docker tag ${FRONTEND_IMAGE}:${resolvedTag} ${FRONTEND_IMAGE}:latest
                                docker tag ${GITOPS_AGENT_IMAGE}:${resolvedTag} ${GITOPS_AGENT_IMAGE}:latest
                                docker tag ${PORTAINER_AGENT_IMAGE}:${resolvedTag} ${PORTAINER_AGENT_IMAGE}:latest
                                docker tag ${BUILD_AGENT_IMAGE}:${resolvedTag} ${BUILD_AGENT_IMAGE}:latest
                                docker tag ${GATEWAY_IMAGE}:${resolvedTag} ${GATEWAY_IMAGE}:latest
                                docker tag ${EMBED_GATEWAY_IMAGE}:${resolvedTag} ${EMBED_GATEWAY_IMAGE}:latest
                                push_with_retry ${BACKEND_IMAGE}:latest
                                push_with_retry ${FRONTEND_IMAGE}:latest
                                push_with_retry ${GITOPS_AGENT_IMAGE}:latest
                                push_with_retry ${PORTAINER_AGENT_IMAGE}:latest
                                push_with_retry ${BUILD_AGENT_IMAGE}:latest
                                push_with_retry ${GATEWAY_IMAGE}:latest
                                push_with_retry ${EMBED_GATEWAY_IMAGE}:latest
                                docker rmi ${BACKEND_IMAGE}:${resolvedTag} ${FRONTEND_IMAGE}:${resolvedTag} ${GITOPS_AGENT_IMAGE}:${resolvedTag} ${PORTAINER_AGENT_IMAGE}:${resolvedTag} ${BUILD_AGENT_IMAGE}:${resolvedTag} ${GATEWAY_IMAGE}:${resolvedTag} ${EMBED_GATEWAY_IMAGE}:${resolvedTag} || true
                            """
                            // The box image carries BOTH the build tag and :v1. The
                            // catalog entry names :v1, so a box pod pulls whatever :v1
                            // last pointed at; the build tag exists so an operator can
                            // say which build a live box actually came from. It is
                            // NOT tagged :latest — nothing pulls :latest, and a third
                            // moving tag is a third thing to disagree.
                            if (params.BUILD_BOX_IMAGE) {
                                sh """
                                    set -eux
${PUSH_WITH_RETRY_SH}
                                    push_with_retry ${BOX_WARM_IMAGE}:${resolvedTag}
                                    docker tag ${BOX_WARM_IMAGE}:${resolvedTag} ${BOX_WARM_IMAGE}:v1
                                    push_with_retry ${BOX_WARM_IMAGE}:v1
                                    docker rmi ${BOX_WARM_IMAGE}:${resolvedTag} || true
                                """
                            }
                        }
                    }

                    // GitOps write-back: pin the built tag into the ArgoCD source
                    // so prod rolls. No image-updater exists — this commit IS the
                    // deploy trigger. Uses the same gh-token PAT as the registry push.
                    runStage('GitOps write-back') {
                        withCredentials([usernamePassword(
                                credentialsId: 'gh-token',
                                usernameVariable: 'GIT_USERNAME',
                                passwordVariable: 'GIT_TOKEN'
                        )]) {
                            sh """
                                set -eu
                                apk add --no-cache git yq >/dev/null 2>&1 || true
                                rm -rf /tmp/argo-infra
                                git clone --depth 1 --branch ${ARGO_BRANCH} \
                                  https://\${GIT_USERNAME}:\${GIT_TOKEN}@${ARGO_REPO} /tmp/argo-infra
                                cd /tmp/argo-infra
                                export TAG='${resolvedTag}'
                                yq -i '(.backend.image.tag, .frontend.image.tag, .gateway.image.tag, .gitopsAgent.image.tag, .portainerAgent.image.tag, .buildAgent.image.tag) = strenv(TAG) | (.backend.image.tag, .frontend.image.tag, .gateway.image.tag, .gitopsAgent.image.tag, .portainerAgent.image.tag, .buildAgent.image.tag) style="double"' ${ARGO_VALUES_PATH}
                                git config user.email 'platform-bot@dada-tuda.ru'
                                git config user.name  'DADA Platform Bot'
                                if git diff --quiet -- ${ARGO_VALUES_PATH}; then
                                    echo "Tag already ${resolvedTag} — nothing to write back"
                                else
                                    git add ${ARGO_VALUES_PATH}
                                    git commit -m "deploy(cloud-console): pin to ${resolvedTag} (build #${env.BUILD_NUMBER})"
                                    git push origin ${ARGO_BRANCH}
                                    echo "Wrote ${resolvedTag} -> ${ARGO_VALUES_PATH} on ${ARGO_BRANCH}"
                                fi
                            """
                        }
                    }

                    runStage('E2E smoke') {
                        container('playwright') {
                            dir('frontend') {
                                catchError(buildResult: 'UNSTABLE', stageResult: 'FAILURE', message: 'e2e smoke failed (non-blocking; runs against the currently-live console)') {
                                    sh '''
                                        set -eux
                                        npx playwright install chromium
                                        E2E_BASE_URL=https://console.dada-tuda.ru E2E_MARKETING_BASE_URL=https://cloud.dada-tuda.ru npx playwright test --project=smoke
                                    '''
                                }
                            }
                        }
                    }

                    if (params.RUN_E2E_AUTHED) {
                        runStage('E2E authed') {
                            container('playwright') {
                                dir('frontend') {
                                    withCredentials([
                                            usernamePassword(credentialsId: 'e2e-console-user', usernameVariable: 'E2E_USER', passwordVariable: 'E2E_PASS'),
                                            string(credentialsId: 'e2e-project-id', variable: 'E2E_PROJECT_ID')
                                    ]) {
                                        sh '''
                                            set -eu
                                            npx playwright install chromium
                                            E2E_BASE_URL=https://console.dada-tuda.ru E2E_MUTATE=1 npx playwright test --project=setup --project=authed
                                        '''
                                    }
                                }
                            }
                        }
                    }
                } else {
                    echo "Docker push skipped (PR or non-deploy branch)"
                }
            }

        } catch (err) {
            currentBuild.result = 'FAILURE'
            currentBuild.description = (currentStageName == 'Docker push' || currentStageName == 'GitOps write-back')
                    ? "FAILED AT PUBLISH (${currentStageName}) — code and tests passed"
                    : "FAILED AT ${currentStageName}"
            throw err
        }

        if (currentBuild.result != 'FAILURE') {
            echo "✅ DADA Cloud Console — ${resolvedTag}"
            echo "   Backend:         ${BACKEND_IMAGE}:${resolvedTag}"
            echo "   Frontend:        ${FRONTEND_IMAGE}:${resolvedTag}"
            echo "   GitOps Agent:    ${GITOPS_AGENT_IMAGE}:${resolvedTag}"
            echo "   Portainer Agent: ${PORTAINER_AGENT_IMAGE}:${resolvedTag}"
            echo "   Build Agent:     ${BUILD_AGENT_IMAGE}:${resolvedTag}"
        }
    }
    }
}
