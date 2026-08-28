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
def TG_GATEWAY_IMAGE      = "${GITHUB_REGISTRY}/${GITHUB_ORG}/dada-cloud-console-tg-gateway"
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

// abortPrevious tried TWICE now, reverted both times for the same reason:
// starvation. First (#813-#818): killed queued builds before GitOps
// write-back ran. Second (2026-08-22, this window, builds #1309-#1319): main
// gets pushed by several parallel sessions every 3-8 min, and the pipeline
// (7-way parallel Docker build+push, one shared dind) takes longer than that
// to finish — every build got aborted by the next push before reaching
// write-back, so literally NOTHING shipped for 90+ minutes. Moving write-back
// earlier does not fix this: it only helps when pushes are rare enough for a
// build to finish between them, and on a shared main with multiple agents
// pushing they are not. Queueing (plain disableConcurrentBuilds, no
// abortPrevious) trades a backlog for guaranteed forward progress — do not
// re-enable abortPrevious without first checking actual push cadence on main.
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
  #
  # POD MEMORY TOTAL IS SIZED AGAINST THOSE TWO NODES, NOT THE CLUSTER'S BIGGEST.
  # Both 15Gi nodes carry a databases/postgresql pod, so this fence removes them
  # both; what is left are the two 12Gi nodes, whose free (unrequested) memory was
  # 4104Mi and 3828Mi when this was measured. Build #1285 reserved 4672Mi and sat
  # Pending forever; #1286 reserved 4512Mi after a trim measured against the 15Gi
  # node (4666Mi free) — a node this fence forbids — and was Pending just the same.
  # The container requests below now total 3840Mi. Anything that raises the pod
  # total must be checked against the free memory of the SMALLEST eligible node,
  # or the build stops starting rather than starts failing.
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
          memory: "192Mi"
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
          memory: "192Mi"
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
          # 1792Mi (was 2Gi == limit). request == limit was chosen because at
          # request 256Mi this container sat far over request during the build and
          # was the kubelet's first eviction victim under node MemoryPressure
          # (#136/#139). That protection is kept in substance: NODE_OPTIONS caps
          # the heap at 1536Mi, so 1792Mi still covers heap + runtime overhead and
          # the container is not meaningfully over request. The 256Mi given back is
          # part of the 672Mi the pod had to shed to fit an eligible node at all —
          # see the pod-total note on the anti-affinity block above.
          memory: "1792Mi"
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
          # 128Mi (was 256Mi): the container idles outside the e2e stages and has
          # never been an eviction victim -- its limit is 12x its request and it
          # survived every build at that ratio. The pod's total reservation
          # (4672Mi) exceeded the free memory of the largest schedulable node
          # (4666Mi) by 6Mi, so build #1285 sat Pending indefinitely with the
          # console promo fix undelivered. Trimmed here and on docker to buy
          # headroom without touching node-builder/dind, whose request==limit is
          # load-bearing (see #136/#139 and #138/#141 below).
          cpu: "50m"
          memory: "96Mi"
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
          # 32Mi (was 64Mi): this is the docker CLI, not the daemon -- it execs a
          # short-lived client and the daemon's memory lives in dind.
          memory: "32Mi"
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
          # 1280Mi (limit stays 1536Mi). dind burns memory building/exporting the
          # 4 images; at request 512Mi it ran far over request and was evicted
          # under node MemoryPressure (#138/#141 die in the docker stage). 1280Mi
          # keeps the reservation within 256Mi of the limit — the eviction ranking
          # is by usage-over-request, and at this ratio dind is no longer the
          # cheapest victim. Part of the 672Mi the pod shed to become schedulable.
          memory: "1280Mi"
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
        def deployedThisBuild = false
        def writebackSha = ''

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

                        // Backend tests moved out of this lane: they now run AFTER
                        // GitOps write-back, non-blocking to the deploy (see the
                        // standalone "Backend tests" stage near the bottom of this
                        // file). go test does not gate go build, so nothing here
                        // depended on it running first.

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

                        stage('TG-Gateway build') {
                            dir('backend') {
                                sh 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags="-s -w" -o bin/tg-gateway ./cmd/tg-gateway'
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
                                  --set tgGateway.image.tag=${resolvedTag} \
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

                        stage('Frontend typecheck + tests') {
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
                                    '''
                                }
                            }
                        }

                        stage('Frontend build') {
                            dir('frontend') {
                                // Moved out of frontend/Dockerfile's builder stage (2026-08-22):
                                // that made the frontend Docker branch the slowest of the 7-way
                                // parallel Docker build+push (npm ci + next build inside dind,
                                // ~2m30s vs ~1m30s for the prebuilt-binary Go images), so it set
                                // the pace for the whole stage every build. Built here instead,
                                // concurrently with the Go lane above (this parallel() block is a
                                // barrier the Docker stage waits on regardless) — net wall-clock
                                // wash, but it stops being the long pole once Docker build+push
                                // starts. Dockerfile now just COPYs .next/standalone from this
                                // same shared workspace-volume (node-builder and the docker/dind
                                // containers all mount it) instead of rebuilding from source.
                                withEnv([
                                    "NEXT_PUBLIC_AUTH_MODE=${NEXT_PUBLIC_AUTH_MODE}",
                                    "NEXT_PUBLIC_KEYCLOAK_ISSUER=${NEXT_PUBLIC_KEYCLOAK_ISSUER}",
                                    "NEXT_PUBLIC_OIDC_CLIENT_ID=${NEXT_PUBLIC_OIDC_CLIENT_ID}",
                                    "NEXT_PUBLIC_CONSOLE_URL=${NEXT_PUBLIC_CONSOLE_URL}",
                                ]) {
                                    sh '''
                                        set -eux
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
                // Push only on integration branches, not PRs
                def isPullRequest = (env.CHANGE_ID != null && env.CHANGE_ID != '')
                def shouldPush = !isPullRequest && (
                        env.BRANCH_NAME == 'main' ||
                        env.BRANCH_NAME == 'master' ||
                        env.BRANCH_NAME == 'develop'
                )

                // Per-image build-then-push (user call: waiting for all 6-7
                // builds before pushing any is pure serialization when each
                // image is independent). Confirmed LIVE on build #1318
                // (2026-08-22): running all 7 as one parallel() OOMKilled the
                // shared dind container 4 TIMES in a single build (dind limit
                // is 1536Mi, see pod template above) — each OOMKill took the
                // whole agent pod offline and Jenkins rescheduled the pipeline
                // onto a fresh pod, replaying Checkout from scratch. Same build
                // number, not abortPrevious, not a different build — visible in
                // Blue Ocean as one lane repeating checkout->build->die 4x.
                // The node this pod lands on has ~4.6-5Gi free total (see the
                // #1285 Pending note near node-builder above) so bumping dind's
                // memory further makes the pod unschedulable instead — not an
                // option. Fix is concurrency, not memory: batch the images
                // BATCH_SIZE at a time below. Each image still builds+pushes
                // independently within its batch (no waiting on a SPECIFIC
                // other image), but dind never has to hold more than
                // BATCH_SIZE builds' memory at once.
                runStage('Docker build+push') {
                    if (shouldPush) {
                        withCredentials([usernamePassword(
                                credentialsId: 'gh-token',
                                usernameVariable: 'GITHUB_USERNAME',
                                passwordVariable: 'GITHUB_TOKEN'
                        )]) {
                            sh "echo \"\${GITHUB_TOKEN}\" | docker login ${GITHUB_REGISTRY} -u \${GITHUB_USERNAME} --password-stdin"
                        }
                    }

                    def targets = [
                            [name: 'backend', image: BACKEND_IMAGE, dockerfile: 'backend/Dockerfile', context: 'backend'],
                            [name: 'gateway', image: GATEWAY_IMAGE, dockerfile: 'backend/Dockerfile.gateway', context: 'backend'],
                            [name: 'tg-gateway', image: TG_GATEWAY_IMAGE, dockerfile: 'backend/Dockerfile.tg-gateway', context: 'backend'],
                            [name: 'embed-gateway', image: EMBED_GATEWAY_IMAGE, dockerfile: 'backend/Dockerfile.grafana-embed-gateway', context: 'backend'],
                            [name: 'gitops-agent', image: GITOPS_AGENT_IMAGE, dockerfile: 'gitops-agent/Dockerfile', context: 'gitops-agent'],
                            [name: 'portainer-agent', image: PORTAINER_AGENT_IMAGE, dockerfile: 'portainer-agent/Dockerfile', context: 'portainer-agent'],
                            [name: 'build-agent', image: BUILD_AGENT_IMAGE, dockerfile: 'build-agent/Dockerfile', context: 'build-agent'],
                    ]

                    def branches = [:]
                    // def target = t rebind is required: Groovy/CPS closures
                    // capture the loop VARIABLE, not its value — without the
                    // rebind every branch below would build/push whatever
                    // target the loop landed on last.
                    for (t in targets) {
                        def target = t
                        branches[target.name] = {
                            sh """
                                set -eux
                                docker build -t ${target.image}:${resolvedTag} -f ${target.dockerfile} ${target.context}
                            """
                            if (shouldPush) {
                                retry(2) {
                                    sh """
                                        set -eux
${PUSH_WITH_RETRY_SH}
                                        push_with_retry ${target.image}:${resolvedTag}
                                        docker tag ${target.image}:${resolvedTag} ${target.image}:latest
                                        push_with_retry ${target.image}:latest
                                        docker rmi ${target.image}:${resolvedTag} || true
                                    """
                                }
                            }
                        }
                    }

                    // Frontend image: npm ci inside the build pulls @dada/* from
                    // Nexus. Auth is passed as a BuildKit secret (never baked into
                    // a layer). NEXT_PUBLIC_* are build args (baked — they are
                    // public). Own branch: different secret/build-arg shape than
                    // the prebuilt-binary images above, and its own auth temp
                    // file (parallel branches share the container filesystem).
                    branches['frontend'] = {
                        // Build already happened in the 'Frontend build' stage (node-builder,
                        // parallel with the Go backend lane) — this is copy-only now, no
                        // npm ci/build inside dind, no Nexus secret, no NEXT_PUBLIC_* args
                        // (baked in at npm build time already). See frontend/Dockerfile.
                        sh """
                            set -eu
                            docker build \\
                              -t ${FRONTEND_IMAGE}:${resolvedTag} \\
                              -f frontend/Dockerfile frontend
                        """
                        if (shouldPush) {
                            retry(2) {
                                sh """
                                    set -eux
${PUSH_WITH_RETRY_SH}
                                    push_with_retry ${FRONTEND_IMAGE}:${resolvedTag}
                                    docker tag ${FRONTEND_IMAGE}:${resolvedTag} ${FRONTEND_IMAGE}:latest
                                    push_with_retry ${FRONTEND_IMAGE}:latest
                                    docker rmi ${FRONTEND_IMAGE}:${resolvedTag} || true
                                """
                            }
                        }
                    }

                    if (params.BUILD_BOX_IMAGE) {
                        // The box image carries BOTH the build tag and :v1. The
                        // catalog entry names :v1, so a box pod pulls whatever :v1
                        // last pointed at; the build tag exists so an operator can
                        // say which build a live box actually came from. It is
                        // NOT tagged :latest — nothing pulls :latest, and a third
                        // moving tag is a third thing to disagree.
                        branches['box-warm'] = {
                            sh """
                                set -eux
                                docker build -t ${BOX_WARM_IMAGE}:${resolvedTag} -f backend/Dockerfile.box-warm backend
                            """
                            if (shouldPush) {
                                retry(2) {
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
                    }

                    // subList() returns a non-serializable ArrayList$SubList view --
                    // CPS checkpoints pipeline state after every step and blew up on
                    // it (build #1321: NotSerializableException, whole run FAILURE).
                    // Copy each slice into a plain (serializable) list instead.
                    def BATCH_SIZE = 3
                    def branchNames = branches.keySet().toList()
                    def i = 0
                    while (i < branchNames.size()) {
                        def batchMap = [:]
                        def end = Math.min(i + BATCH_SIZE, branchNames.size())
                        for (int j = i; j < end; j++) {
                            def name = branchNames[j]
                            batchMap[name] = branches[name]
                        }
                        parallel batchMap
                        i = end
                    }
                }

                if (shouldPush) {
                    // GitOps write-back: pin the built tag into the ArgoCD source
                    // so prod rolls. No image-updater exists — this commit IS the
                    // deploy trigger. Uses the same gh-token PAT as the registry push.
                    //
                    // retry(3): wraps the FULL clone-edit-commit-push, not just the
                    // push. A bare push retry would replay a stale commit against a
                    // branch that moved under it (two builds writing console-migration
                    // concurrently); re-cloning first means each attempt re-diffs
                    // against the CURRENT branch head, so the retry is racing the
                    // branch correctly instead of fighting it (mirrors the empty-diff
                    // guard already in this block, which makes a second successful
                    // attempt a no-op instead of a duplicate commit).
                    runStage('GitOps write-back') {
                        retry(3) {
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
                                yq -i '(.backend.image.tag, .frontend.image.tag, .gateway.image.tag, .tgGateway.image.tag, .gitopsAgent.image.tag, .portainerAgent.image.tag, .buildAgent.image.tag) = strenv(TAG) | (.backend.image.tag, .frontend.image.tag, .gateway.image.tag, .tgGateway.image.tag, .gitopsAgent.image.tag, .portainerAgent.image.tag, .buildAgent.image.tag) style="double"' ${ARGO_VALUES_PATH}
                                git config user.email 'platform-bot@dada-tuda.ru'
                                git config user.name  'DADA Platform Bot'
                                if git diff --quiet -- ${ARGO_VALUES_PATH}; then
                                    echo "Tag already ${resolvedTag} — nothing to write back"
                                else
                                    git add ${ARGO_VALUES_PATH}
                                    git commit -m "deploy(cloud-console): pin to ${resolvedTag} (build #${env.BUILD_NUMBER})"
                                    git rev-parse HEAD > /tmp/argo-infra/.CI_WROTE_SHA
                                    git push origin ${ARGO_BRANCH}
                                    echo "Wrote ${resolvedTag} -> ${ARGO_VALUES_PATH} on ${ARGO_BRANCH}"
                                fi
                            """
                        }
                        }
                    }
                    deployedThisBuild = true
                    // Only set when this build actually wrote a NEW commit (the
                    // empty-diff no-op branch above never creates .CI_WROTE_SHA) —
                    // the catch block below reverts by this SHA, and reverting a
                    // stale/foreign SHA left over from nothing-to-do is wrong.
                    writebackSha = sh(
                            script: 'test -f /tmp/argo-infra/.CI_WROTE_SHA && cat /tmp/argo-infra/.CI_WROTE_SHA || true',
                            returnStdout: true
                    ).trim()

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

            // Runs AFTER GitOps write-back on push branches: prod is already
            // rolling on the tag this build produced by the time internal/api's
            // 92s DB-integration suite starts. That is the deliberate trade for
            // abortPrevious above — moving the slowest gate off the front of the
            // pipeline shrinks the window where an abort throws away a build that
            // had already written back its tag. A red result here means bad code
            // is already deployed, not that it was caught in time; the catch
            // block below labels that case explicitly instead of the misleading
            // "FAILED AT PUBLISH". On PR/non-push branches this is just the
            // ordinary test gate — nothing is deployed either way.
            container('go-builder') {
                runStage('Backend tests') {
                    dir('backend') {
                        withEnv(['TEST_DATABASE_URL=postgres://dada:dada@localhost:5432/dada_test?sslmode=disable']) {
                            sh 'go run ./cmd/migrate'
                            sh 'go test ./... -count=1'
                        }
                    }
                }
            }

        } catch (err) {
            def autoReverted = false
            if (currentStageName == 'Backend tests' && deployedThisBuild && writebackSha) {
                // Backend tests failed AFTER the tag was already pinned and prod
                // started rolling: revert just that write-back commit so prod
                // rolls back to whatever tag was live before this build, instead
                // of sitting on code its own tests just failed. Scoped to the
                // single commit this build made (writebackSha), not a hard reset
                // — safe even if another build's commit landed on the branch
                // after ours, same reasoning as the empty-diff guard above.
                try {
                    container('docker') {
                        withCredentials([usernamePassword(
                                credentialsId: 'gh-token',
                                usernameVariable: 'GIT_USERNAME',
                                passwordVariable: 'GIT_TOKEN'
                        )]) {
                            sh """
                                set -eu
                                cd /tmp/argo-infra
                                git config user.email 'platform-bot@dada-tuda.ru'
                                git config user.name  'DADA Platform Bot'
                                git revert --no-edit ${writebackSha}
                                git push https://\${GIT_USERNAME}:\${GIT_TOKEN}@${ARGO_REPO} HEAD:${ARGO_BRANCH}
                            """
                        }
                    }
                    autoReverted = true
                } catch (rollbackErr) {
                    echo "auto-revert of ${writebackSha} failed: ${rollbackErr}"
                }
            }
            currentBuild.description = (currentStageName == 'Docker build+push' || currentStageName == 'GitOps write-back')
                    ? "FAILED AT PUBLISH (${currentStageName}) — code and tests passed"
                    : (currentStageName == 'Backend tests' && deployedThisBuild)
                            ? (autoReverted
                                    ? "DEPLOYED ${resolvedTag} then REVERTED — Backend tests failed after write-back"
                                    : "DEPLOYED ${resolvedTag} — Backend tests failed after write-back, auto-revert FAILED, check prod")
                            : "FAILED AT ${currentStageName}"
            throw err
        }

        if (currentBuild.result != 'FAILURE') {
            currentBuild.description = "OK ${resolvedTag}"
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
